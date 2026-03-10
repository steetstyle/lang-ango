/*
 * Lang-Ango eBPF SQL Server (TDS) Protocol Tracer
 * Detects SQL Server connections via TDS protocol at port 1433.
 * Query text extraction is not done in the BPF program to avoid
 * variable-offset stack reads which the BPF verifier rejects.
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

#define TDS_PACKET_TYPE_SQL_BATCH  0x01
#define TDS_PACKET_TYPE_RPC        0x03
#define TDS_PACKET_TYPE_RESPONSE   0x04

struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, __u64);
    __type(value, struct request_info);
    __uint(max_entries, 8192);
} tds_connections SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    __uint(max_entries, 128 * 1024);
} db_events SEC(".maps");

SEC("kprobe/tcp_sendmsg")
int BPF_PROG(tcp_sendmsg_tds, struct sock *sk, struct msghdr *msg, size_t size) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    __u32 tid = bpf_get_current_pid_tgid();
    __u64 ts  = bpf_ktime_get_ns();

    struct inet_sock *isk = (struct inet_sock *)sk;

    __u16 dport_raw = 0, sport_raw = 0;
    bpf_probe_read_kernel(&dport_raw, sizeof(dport_raw), &isk->inet_dport);
    bpf_probe_read_kernel(&sport_raw, sizeof(sport_raw), &isk->inet_sport);

    __u16 dport = bpf_ntohs(dport_raw);
    __u16 sport = bpf_ntohs(sport_raw);

    if (dport != 1433 && sport != 1433)
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

    /* Read only the 1-byte TDS packet type header — avoid variable-index
     * stack reads that the BPF verifier rejects. */
    __u8 pkt_type = 0;
    bpf_probe_read_user(&pkt_type, sizeof(pkt_type), iov_base);

    if (pkt_type != TDS_PACKET_TYPE_SQL_BATCH &&
        pkt_type != TDS_PACKET_TYPE_RPC)
        return 0;

    __u32 saddr = 0, daddr = 0;
    bpf_probe_read_kernel(&saddr, sizeof(saddr), &isk->inet_saddr);
    bpf_probe_read_kernel(&daddr, sizeof(daddr), &isk->inet_daddr);

    struct db_event *event = bpf_ringbuf_reserve(&db_events, sizeof(*event), 0);
    if (!event)
        return 0;

    event->pid = pid;
    event->tid = tid;
    event->timestamp_ns = ts;
    event->direction = DIR_OUTGOING;
    event->protocol = PROTO_MSSQL;
    event->event_type = EVENT_DB_QUERY;
    event->saddr = saddr;
    event->daddr = daddr;
    event->sport = sport;
    event->dport = dport;
    event->duration_ns = 0;
    event->query_len = 0;

    bpf_ringbuf_submit(event, 0);

    struct request_info req = {
        .start_time_ns = ts,
        .pid = pid, .tid = tid,
        .content_length = size,
        .direction = DIR_OUTGOING,
    };
    __u64 key = ((__u64)pid << 32) | (__u64)dport;
    bpf_map_update_elem(&tds_connections, &key, &req, BPF_ANY);

    return 0;
}

SEC("kprobe/tcp_recvmsg")
int BPF_PROG(tcp_recvmsg_tds, struct sock *sk, struct msghdr *msg,
             size_t size, int flags) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    __u64 ts  = bpf_ktime_get_ns();

    struct inet_sock *isk = (struct inet_sock *)sk;

    __u16 dport_raw = 0, sport_raw = 0;
    bpf_probe_read_kernel(&dport_raw, sizeof(dport_raw), &isk->inet_dport);
    bpf_probe_read_kernel(&sport_raw, sizeof(sport_raw), &isk->inet_sport);

    __u16 dport = bpf_ntohs(dport_raw);
    __u16 sport = bpf_ntohs(sport_raw);

    if (dport != 1433 && sport != 1433)
        return 0;

    __u64 key = ((__u64)pid << 32) | (__u64)((sport == 1433) ? dport : sport);
    struct request_info *req = bpf_map_lookup_elem(&tds_connections, &key);
    if (!req)
        return 0;

    __u64 duration = ts - req->start_time_ns;

    __u32 saddr = 0, daddr = 0;
    bpf_probe_read_kernel(&saddr, sizeof(saddr), &isk->inet_saddr);
    bpf_probe_read_kernel(&daddr, sizeof(daddr), &isk->inet_daddr);

    struct db_event *event = bpf_ringbuf_reserve(&db_events, sizeof(*event), 0);
    if (event) {
        event->pid = pid;
        event->timestamp_ns = ts;
        event->direction = DIR_INCOMING;
        event->protocol = PROTO_MSSQL;
        event->event_type = EVENT_DB_RESPONSE;
        event->duration_ns = duration;
        event->saddr = daddr;
        event->daddr = saddr;
        event->sport = sport;
        event->dport = dport;
        bpf_ringbuf_submit(event, 0);
    }

    bpf_map_delete_elem(&tds_connections, &key);
    return 0;
}

char _license[] SEC("license") = "GPL";
