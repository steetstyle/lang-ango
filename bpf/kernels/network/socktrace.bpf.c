
/*
 * Lang-Ango eBPF Network Socket Tracer
 * Captures HTTP traffic at the socket level.
 * Uses bpf_probe_read_kernel for all kernel struct accesses because
 * kprobe-extracted pointers are treated as scalars by the BPF verifier.
 */

#include <linux/bpf.h>
#include <linux/ptrace.h>
#include <linux/stddef.h>
#include <linux/in.h>
#include <linux/in6.h>
#include <linux/uaccess.h>
#include <linux/socket.h>
#include <net/sock.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>
#include "common.h"

struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, struct conn_key);
    __type(value, struct request_info);
    __uint(max_entries, 65536);
} conn_info SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, __u64);
    __type(value, struct endpoint_metrics);
    __uint(max_entries, 16384);
} endpoint_metrics SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    __uint(max_entries, 256 * 1024);
} http_events SEC(".maps");

/* Safely read the four address/port fields from inet_sock via probe_read. */
static __always_inline void read_inet_addrs(struct inet_sock *isk,
    __u32 *saddr, __u32 *daddr, __u16 *sport, __u16 *dport)
{
    bpf_probe_read_kernel(saddr, sizeof(*saddr), &isk->inet_saddr);
    bpf_probe_read_kernel(daddr, sizeof(*daddr), &isk->inet_daddr);
    bpf_probe_read_kernel(sport, sizeof(*sport), &isk->inet_sport);
    bpf_probe_read_kernel(dport, sizeof(*dport), &isk->inet_dport);
}

/* Read the first iov chunk from a msghdr into buf. */
static __always_inline int read_iov_data(struct msghdr *msg, char *buf, int len)
{
    __kernel_size_t iovlen = 0;
    bpf_probe_read_kernel(&iovlen, sizeof(iovlen), &msg->msg_iovlen);
    if (iovlen == 0)
        return -1;

    struct iovec *iov_ptr = NULL;
    bpf_probe_read_kernel(&iov_ptr, sizeof(iov_ptr), &msg->msg_iov);
    if (!iov_ptr)
        return -1;

    void *iov_base = NULL;
    bpf_probe_read_kernel(&iov_base, sizeof(iov_base), &iov_ptr->iov_base);
    if (!iov_base)
        return -1;

    bpf_probe_read_user(buf, len, iov_base);
    return 0;
}

/*
 * TCP sendmsg entry - capture outgoing HTTP requests
 */
SEC("kprobe/tcp_sendmsg")
int BPF_PROG(tcp_sendmsg_entry, struct sock *sk, struct msghdr *msg,
             size_t size) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    __u32 tid = bpf_get_current_pid_tgid();
    __u64 ts = bpf_ktime_get_ns();

    char buf[64];
    __builtin_memset(buf, 0, sizeof(buf));
    if (read_iov_data(msg, buf, sizeof(buf)) < 0)
        return 0;

    __u8 method = detect_http_method(buf, sizeof(buf));
    if (method == HTTP_METHOD_UNKNOWN)
        return 0;

    struct inet_sock *isk = (struct inet_sock *)sk;
    __u32 saddr = 0, daddr = 0;
    __u16 sport = 0, dport = 0;
    read_inet_addrs(isk, &saddr, &daddr, &sport, &dport);

    /* Skip path extraction — its variable-bound loops exceed the BPF
     * verifier's 1M-instruction limit when MAX_PATH_LEN is large. */

    struct conn_key key = {
        .pid = pid, .tid = tid,
        .saddr = saddr, .daddr = daddr,
        .sport = bpf_ntohs(sport), .dport = bpf_ntohs(dport),
        .protocol = PROTO_HTTP,
    };

    struct request_info req = {
        .start_time_ns = ts,
        .pid = pid, .tid = tid,
        .saddr = saddr, .daddr = daddr,
        .sport = bpf_ntohs(sport), .dport = bpf_ntohs(dport),
        .content_length = size,
        .direction = DIR_OUTGOING,
        .method = method,
    };
    bpf_map_update_elem(&conn_info, &key, &req, BPF_ANY);

    struct http_event *event = bpf_ringbuf_reserve(&http_events, sizeof(*event), 0);
    if (!event)
        return 0;

    event->pid = pid;
    event->tid = tid;
    event->timestamp_ns = ts;
    event->direction = DIR_OUTGOING;
    event->protocol = PROTO_HTTP;
    event->event_type = EVENT_HTTP_REQUEST;
    event->method = method;
    event->request_len = size;
    event->saddr = saddr;
    event->daddr = daddr;
    event->sport = bpf_ntohs(sport);
    event->dport = bpf_ntohs(dport);
    event->path_len = 0;

    bpf_ringbuf_submit(event, 0);
    return 0;
}

/*
 * TCP recvmsg entry - capture incoming HTTP responses
 */
SEC("kprobe/tcp_recvmsg")
int BPF_PROG(tcp_recvmsg_entry, struct sock *sk, struct msghdr *msg,
             size_t size, int flags) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    __u32 tid = bpf_get_current_pid_tgid();
    __u64 ts = bpf_ktime_get_ns();

    struct inet_sock *isk = (struct inet_sock *)sk;
    __u32 saddr = 0, daddr = 0;
    __u16 sport = 0, dport = 0;
    read_inet_addrs(isk, &saddr, &daddr, &sport, &dport);

    struct conn_key key = {
        .pid = pid, .tid = tid,
        .saddr = daddr, .daddr = saddr,
        .sport = bpf_ntohs(dport), .dport = bpf_ntohs(sport),
        .protocol = PROTO_HTTP,
    };

    struct request_info *req = bpf_map_lookup_elem(&conn_info, &key);
    if (!req) {
        key.saddr = saddr; key.daddr = daddr;
        key.sport = bpf_ntohs(sport); key.dport = bpf_ntohs(dport);
        req = bpf_map_lookup_elem(&conn_info, &key);
        if (!req)
            return 0;
    }

    char buf[64];
    __builtin_memset(buf, 0, sizeof(buf));
    read_iov_data(msg, buf, sizeof(buf));

    __u16 status = detect_http_status(buf, sizeof(buf));
    __u64 duration = ts - req->start_time_ns;
    __u8  req_method = req->method;

    struct http_event *event = bpf_ringbuf_reserve(&http_events, sizeof(*event), 0);
    if (!event)
        return 0;

    event->pid = pid;
    event->tid = tid;
    event->timestamp_ns = ts;
    event->direction = DIR_INCOMING;
    event->protocol = PROTO_HTTP;
    event->event_type = EVENT_HTTP_RESPONSE;
    event->status = status;
    event->response_len = size;
    event->duration_ns = duration;
    event->saddr = saddr;
    event->daddr = daddr;
    event->sport = bpf_ntohs(sport);
    event->dport = bpf_ntohs(dport);
    event->method = req_method;

    __u64 ep_key = (__u64)saddr ^ (__u64)daddr ^
                   ((__u64)bpf_ntohs(sport) << 16) ^
                   ((__u64)bpf_ntohs(dport) << 32);
    struct endpoint_metrics *metrics = bpf_map_lookup_elem(&endpoint_metrics, &ep_key);
    if (metrics) {
        __sync_fetch_and_add(&metrics->request_count, 1);
        __sync_fetch_and_add(&metrics->total_duration_ns, duration);
        if (status >= 400)
            __sync_fetch_and_add(&metrics->error_count, 1);
    }

    bpf_map_delete_elem(&conn_info, &key);
    bpf_ringbuf_submit(event, 0);
    return 0;
}

/*
 * Inet bind hook - track new listening ports
 */
SEC("kprobe/inet_bind")
int BPF_PROG(inet_bind, struct socket *sock, struct sockaddr *addr, int addr_len) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;

    if (!addr || addr_len < (int)sizeof(struct sockaddr_in))
        return 0;

    struct sockaddr_in addr4;
    if (bpf_probe_read_kernel(&addr4, sizeof(addr4), addr) < 0)
        return 0;

    if (addr4.sin_family != AF_INET)
        return 0;

    __u16 port = bpf_ntohs(addr4.sin_port);
    __u64 key = (__u64)pid | ((__u64)port << 32);
    struct endpoint_metrics metrics = {};
    bpf_map_update_elem(&endpoint_metrics, &key, &metrics, BPF_NOEXIST);

    return 0;
}

char _license[] SEC("license") = "GPL";
