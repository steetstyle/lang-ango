/*
 * Lang-Ango eBPF Network Socket Tracer
 * Captures HTTP/SQL/etc traffic at the socket level
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

/* Map definitions */
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
} events SEC(".maps");

/*
 * TCP sendmsg entry - capture outgoing requests
 */
SEC("kprobe/tcp_sendmsg")
int BPF_PROG(tcp_sendmsg_entry, struct sock *sk, struct msghdr *msg, 
             size_t size) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    __u32 tid = bpf_get_current_pid_tgid();
    __u64 ts = bpf_ktime_get_ns();
    
    // Get socket addresses
    struct sockaddr *addr = msg->msg_name;
    if (!addr)
        return 0;
        
    struct inet_sock *isk = (struct inet_sock *)sk;
    
    __u32 daddr = isk->inet_daddr;
    __u16 sport = isk->inet_sport;
    __u16 dport = isk->inet_dport;
    
    // Get I/O vector data
    struct iovec *iov = msg->msg_iov;
    if (!iov || msg->msg_iovlen == 0)
        return 0;
        
    // Read first iov chunk
    char buf[256];
    __builtin_memset(buf, 0, sizeof(buf));
    bpf_probe_read_user(buf, sizeof(buf), iov->iov_base);
    
    // Check if HTTP
    __u8 method = detect_http_method(buf, sizeof(buf));
    if (method == HTTP_METHOD_UNKNOWN)
        return 0;
    
    // Extract path
    char path[MAX_PATH_LEN];
    __u16 path_len = 0;
    extract_path(buf, sizeof(buf), path, &path_len);
    
    // Create connection key
    struct conn_key key = {
        .pid = pid,
        .tid = tid,
        .saddr = isk->inet_saddr,
        .daddr = daddr,
        .sport = bpf_ntohs(sport),
        .dport = bpf_ntohs(dport),
        .protocol = PROTO_HTTP
    };
    
    // Store request info
    struct request_info req = {
        .start_time_ns = ts,
        .pid = pid,
        .tid = tid,
        .saddr = isk->inet_saddr,
        .daddr = daddr,
        .sport = bpf_ntohs(sport),
        .dport = bpf_ntohs(dport),
        .content_length = size,
        .direction = DIR_OUTGOING,
        .method = method
    };
    
    bpf_map_update_elem(&conn_info, &key, &req, BPF_ANY);
    
    // Send event to ring buffer
    struct http_event *event = bpf_ringbuf_reserve(&events, sizeof(*event), 0);
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
    event->saddr = isk->inet_saddr;
    event->daddr = daddr;
    event->sport = bpf_ntohs(sport);
    event->dport = bpf_ntohs(dport);
    
    if (path_len > 0) {
        __builtin_memcpy(event->path, path, path_len);
        event->path_len = path_len;
    }
    
    bpf_ringbuf_submit(event, 0);
    
    return 0;
}

/*
 * TCP recvmsg entry - capture incoming responses
 */
SEC("kprobe/tcp_recvmsg")
int BPF_PROG(tcp_recvmsg_entry, struct sock *sk, struct msghdr *msg,
             size_t size, int flags) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    __u32 tid = bpf_get_current_pid_tgid();
    __u64 ts = bpf_ktime_get_ns();
    
    struct inet_sock *isk = (struct inet_sock *)sk;
    
    __u32 saddr = isk->inet_saddr;
    __u32 daddr = isk->inet_daddr;
    __u16 sport = isk->inet_sport;
    __u16 dport = isk->inet_dport;
    
    // Try to match with stored request
    struct conn_key key = {
        .pid = pid,
        .tid = tid,
        .saddr = daddr,  // Remote is source for response
        .daddr = saddr,
        .sport = bpf_ntohs(dport),
        .dport = bpf_ntohs(sport),
        .protocol = PROTO_HTTP
    };
    
    struct request_info *req = bpf_map_lookup_elem(&conn_info, &key);
    if (!req) {
        // Try reverse direction
        key.saddr = saddr;
        key.daddr = daddr;
        key.sport = bpf_ntohs(sport);
        key.dport = bpf_ntohs(dport);
        req = bpf_map_lookup_elem(&conn_info, &key);
        if (!req)
            return 0;
    }
    
    // Read response data
    char buf[256];
    __builtin_memset(buf, 0, sizeof(buf));
    if (msg->msg_iovlen > 0) {
        bpf_probe_read_user(buf, sizeof(buf), msg->msg_iov[0].iov_base);
    }
    
    // Detect HTTP status
    __u16 status = detect_http_status(buf, sizeof(buf));
    __u64 duration = ts - req->start_time_ns;
    
    // Send response event
    struct http_event *event = bpf_ringbuf_reserve(&events, sizeof(*event), 0);
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
    
    // Copy method from request
    event->method = req->method;
    
    // Update endpoint metrics
    __u64 endpoint_key = (__u64)saddr ^ (__u64)daddr ^ 
                         ((__u64)bpf_ntohs(sport) << 16) ^ 
                         ((__u64)bpf_ntohs(dport) << 32);
    
    struct endpoint_metrics *metrics = bpf_map_lookup_elem(&endpoint_metrics, &endpoint_key);
    if (!metrics) {
        struct endpoint_metrics new_metrics = {0};
        bpf_map_update_elem(&endpoint_metrics, &endpoint_key, &new_metrics, BPF_ANY);
        metrics = bpf_map_lookup_elem(&endpoint_metrics, &endpoint_key);
    }
    
    if (metrics) {
        __atomic_add_fetch(&metrics->request_count, 1, __ATOMIC_RELAXED);
        __atomic_add_fetch(&metrics->total_duration_ns, duration, __ATOMIC_RELAXED);
        if (status >= 400) {
            __atomic_add_fetch(&metrics->error_count, 1, __ATOMIC_RELAXED);
        }
        
        // Update histogram bucket
        __u64 bucket = 0;
        if (duration < 1000000) bucket = 0;      // < 1ms
        else if (duration < 5000000) bucket = 1; // < 5ms
        else if (duration < 10000000) bucket = 2; // < 10ms
        else if (duration < 50000000) bucket = 3; // < 50ms
        else if (duration < 100000000) bucket = 4; // < 100ms
        else if (duration < 500000000) bucket = 5; // < 500ms
        else bucket = 6; // >= 500ms
        
        if (bucket < 11) {
            __atomic_add_fetch(&metrics->histogram[bucket], 1, __ATOMIC_RELAXED);
        }
    }
    
    // Cleanup connection tracking
    bpf_map_delete_elem(&conn_info, &key);
    
    bpf_ringbuf_submit(event, 0);
    
    return 0;
}

/*
 * Inet bind hook - detect new listening ports
 */
SEC("kprobe/inet_bind")
int BPF_PROG(inet_bind, struct socket *sock, struct sockaddr *addr, int addr_len) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    
    if (!addr || addr_len < sizeof(struct sockaddr_in))
        return 0;
        
    struct sockaddr_in *addr4 = (struct sockaddr_in *)addr;
    __u16 port = bpf_ntohs(addr4->sin_port);
    
    // Send port event
    struct {
        __u32 pid;
        __u16 port;
        __u8  event_type;
    } port_event = { .pid = pid, .port = port, .event_type = 1 };
    
    __u32 key = port;
    bpf_map_update_elem(&endpoint_metrics, &key, &port_event, BPF_ANY);
    
    return 0;
}

char _license[] SEC("license") = "GPL";
