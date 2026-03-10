/*
 * Lang-Ango eBPF PostgreSQL Protocol Tracer
 * Captures queries on port 5432 (PostgreSQL wire protocol v3).
 *
 * Simple Query message layout (client → server):
 *   Byte1('Q')   — message type
 *   Int32        — message length (4 bytes, big-endian, includes self)
 *   String       — null-terminated SQL query
 *
 * We read 69 bytes: 1 type + 4 length + 64 query chars.
 * All offsets are compile-time constants so the BPF verifier accepts them.
 */

#include <linux/bpf.h>
#include <linux/ptrace.h>
#include <linux/stddef.h>
#include <linux/in.h>
#include <linux/uaccess.h>
#include <linux/socket.h>
#include <net/sock.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>
#include "common.h"

#define PG_PORT          5432
#define PG_MSG_QUERY     'Q'   /* simple query */
#define PG_MSG_PARSE     'P'   /* extended query — parse */
#define PG_MSG_EXECUTE   'E'   /* extended query — execute */
#define PG_SNAP_LEN      69    /* 1 type + 4 len + 64 query bytes */
#define PG_QUERY_MAX     64

struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
    __uint(max_entries, 1);
    __type(key, __u32);
    __type(value, char[PG_SNAP_LEN]);
} pg_snap_buf SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, __u64);
    __type(value, struct request_info);
    __uint(max_entries, 8192);
} pg_connections SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    __uint(max_entries, 256 * 1024);
} db_events SEC(".maps");


SEC("kprobe/tcp_sendmsg")
int BPF_PROG(tcp_sendmsg_pg, struct sock *sk, struct msghdr *msg, size_t size) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    __u32 tid = ((__u32)bpf_get_current_pid_tgid());
    __u64 ts  = bpf_ktime_get_ns();

    struct inet_sock *isk = (struct inet_sock *)sk;

    __u16 dport_raw = 0, sport_raw = 0;
    bpf_probe_read_kernel(&dport_raw, sizeof(dport_raw), &isk->inet_dport);
    bpf_probe_read_kernel(&sport_raw, sizeof(sport_raw), &isk->inet_sport);

    __u16 dport = bpf_ntohs(dport_raw);
    __u16 sport = bpf_ntohs(sport_raw);

    if (dport != PG_PORT && sport != PG_PORT)
        return 0;

    /* Only capture client → server traffic (destination port = 5432). */
    if (dport != PG_PORT)
        return 0;

    __kernel_size_t iovlen = 0;
    bpf_probe_read_kernel(&iovlen, sizeof(iovlen), &msg->msg_iovlen);
    if (iovlen == 0)
        return 0;

    struct iovec *iov_ptr = NULL;
    bpf_probe_read_kernel(&iov_ptr, sizeof(iov_ptr), &msg->msg_iov);
    if (!iov_ptr)
        return 0;

    void *iov_base = NULL;
    bpf_probe_read_kernel(&iov_base, sizeof(iov_base), &iov_ptr->iov_base);
    if (!iov_base)
        return 0;

    /* Read fixed-size snapshot: type byte + 4-byte length + 64 query bytes. */
    __u32 zero = 0;
    char *snap = bpf_map_lookup_elem(&pg_snap_buf, &zero);
    if (!snap)
        return 0;

    #pragma clang loop unroll(full)
    for (int i = 0; i < PG_SNAP_LEN; i++)
        snap[i] = 0;
    bpf_probe_read_user(snap, PG_SNAP_LEN, iov_base);

    __u8 msg_type = snap[0];
    if (msg_type != PG_MSG_QUERY &&
        msg_type != PG_MSG_PARSE &&
        msg_type != PG_MSG_EXECUTE)
        return 0;

    __u32 saddr = 0, daddr = 0;
    bpf_probe_read_kernel(&saddr, sizeof(saddr), &isk->inet_saddr);
    bpf_probe_read_kernel(&daddr, sizeof(daddr), &isk->inet_daddr);

    struct db_event *event = bpf_ringbuf_reserve(&db_events, sizeof(*event), 0);
    if (!event)
        return 0;

    event->pid          = pid;
    event->tid          = tid;
    event->timestamp_ns = ts;
    event->direction    = DIR_OUTGOING;
    event->protocol     = PROTO_POSTGRES;
    event->event_type   = EVENT_DB_QUERY;
    event->saddr        = saddr;
    event->daddr        = daddr;
    event->sport        = sport;
    event->dport        = dport;
    event->duration_ns  = 0;

    /* Copy query text from snap[5..68] (skip 1-byte type + 4-byte length).
     * Using fixed constant offsets avoids variable-offset stack reads that
     * the BPF verifier rejects. */
    bpf_probe_read_kernel(event->query, PG_QUERY_MAX, &snap[5]);
    event->query_len = PG_QUERY_MAX;

    bpf_ringbuf_submit(event, 0);

    /* Track the request for duration measurement. */
    struct request_info req = {
        .start_time_ns = ts,
        .pid = pid, .tid = tid,
        .content_length = size,
        .direction = DIR_OUTGOING,
    };
    __u64 key = ((__u64)pid << 32) | (__u64)dport;
    bpf_map_update_elem(&pg_connections, &key, &req, BPF_ANY);

    return 0;
}

SEC("kprobe/tcp_recvmsg")
int BPF_PROG(tcp_recvmsg_pg, struct sock *sk, struct msghdr *msg,
             size_t size, int flags) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    __u64 ts  = bpf_ktime_get_ns();

    struct inet_sock *isk = (struct inet_sock *)sk;

    __u16 dport_raw = 0, sport_raw = 0;
    bpf_probe_read_kernel(&dport_raw, sizeof(dport_raw), &isk->inet_dport);
    bpf_probe_read_kernel(&sport_raw, sizeof(sport_raw), &isk->inet_sport);

    __u16 dport = bpf_ntohs(dport_raw);
    __u16 sport = bpf_ntohs(sport_raw);

    /* Server response arrives with sport == 5432. */
    if (sport != PG_PORT)
        return 0;

    __u64 key = ((__u64)pid << 32) | (__u64)dport;
    struct request_info *req = bpf_map_lookup_elem(&pg_connections, &key);
    if (!req)
        return 0;

    __u64 duration = ts - req->start_time_ns;

    __u32 saddr = 0, daddr = 0;
    bpf_probe_read_kernel(&saddr, sizeof(saddr), &isk->inet_saddr);
    bpf_probe_read_kernel(&daddr, sizeof(daddr), &isk->inet_daddr);

    struct db_event *event = bpf_ringbuf_reserve(&db_events, sizeof(*event), 0);
    if (event) {
        event->pid          = pid;
        event->timestamp_ns = ts;
        event->direction    = DIR_INCOMING;
        event->protocol     = PROTO_POSTGRES;
        event->event_type   = EVENT_DB_RESPONSE;
        event->duration_ns  = duration;
        event->saddr        = daddr;
        event->daddr        = saddr;
        event->sport        = sport;
        event->dport        = dport;
        event->query_len    = 0;
        bpf_ringbuf_submit(event, 0);
    }

    bpf_map_delete_elem(&pg_connections, &key);
    return 0;
}

char _license[] SEC("license") = "GPL";
