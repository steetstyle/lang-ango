/*
 * Lang-Ango eBPF TLS Interceptor
 * Captures SSL/TLS plaintext before encryption/after decryption
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

/*
 * TLS context tracking map
 * Maps SSL* pointer to connection info
 */
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, __u64);  // SSL pointer
    __type(value, struct request_info);
    __uint(max_entries, 16384);
} tls_context SEC(".maps");

/*
 * TLS events ring buffer
 */
struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    __uint(max_entries, 256 * 1024);
} tls_events SEC(".maps");

/*
 * OpenSSL SSL_write entry - capture plaintext before encryption
 */
SEC("uprobe/SSL_write")
int BPF_PROG(ssl_write_entry, void *ssl, const void *buf, int len) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    __u32 tid = bpf_get_current_pid_tgid();
    __u64 ts = bpf_ktime_get_ns();
    
    if (!buf || len <= 0)
        return 0;
    
    // Read buffer to check if HTTP
    char check_buf[64];
    __builtin_memset(check_buf, 0, sizeof(check_buf));
    bpf_probe_read_user(check_buf, sizeof(check_buf), buf);
    
    // Check if HTTP data
    if (!is_http_data(check_buf, sizeof(check_buf)))
        return 0;
    
    // Store SSL context for matching with write exit
    struct request_info req = {
        .start_time_ns = ts,
        .pid = pid,
        .tid = tid,
        .content_length = len,
        .direction = DIR_OUTGOING
    };
    
    __u64 ssl_ptr = (__u64)ssl;
    bpf_map_update_elem(&tls_context, &ssl_ptr, &req, BPF_ANY);
    
    return 0;
}

/*
 * OpenSSL SSL_write exit - capture after encryption (for timing)
 */
SEC("uretprobe/SSL_write")
int BPF_PROG(ssl_write_exit, void *ssl, int ret) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    __u32 tid = bpf_get_current_pid_tgid();
    __u64 ts = bpf_ktime_get_ns();
    
    __u64 ssl_ptr = (__u64)ssl;
    struct request_info *req = bpf_map_lookup_elem(&tls_context, &ssl_ptr);
    if (!req)
        return 0;
    
    __u64 duration = ts - req->start_time_ns;
    
    // Send TLS event
    struct tls_event *event = bpf_ringbuf_reserve(&tls_events, sizeof(*event), 0);
    if (!event)
        return 0;
        
    event->pid = pid;
    event->tid = tid;
    event->timestamp_ns = ts;
    event->direction = DIR_OUTGOING;
    event->event_type = EVENT_TLS_DATA;
    event->data_len = ret > 0 ? ret : 0;
    event->ssl_ptr = ssl_ptr;
    
    bpf_ringbuf_submit(event, 0);
    
    // Cleanup
    bpf_map_delete_elem(&tls_context, &ssl_ptr);
    
    return 0;
}

/*
 * OpenSSL SSL_read entry - capture after decryption
 */
SEC("uprobe/SSL_read")
int BPF_PROG(ssl_read_entry, void *ssl, void *buf, int len) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    __u32 tid = bpf_get_current_pid_tgid();
    __u64 ts = bpf_ktime_get_ns();
    
    // Store that we're reading
    __u64 ssl_ptr = (__u64)ssl;
    struct request_info req = {
        .start_time_ns = ts,
        .pid = pid,
        .tid = tid,
        .content_length = len,
        .direction = DIR_INCOMING
    };
    
    bpf_map_update_elem(&tls_context, &ssl_ptr, &req, BPF_ANY);
    
    return 0;
}

/*
 * OpenSSL SSL_read exit - capture plaintext after decryption
 */
SEC("uretprobe/SSL_read")
int BPF_PROG(ssl_read_exit, void *ssl, void *buf, int ret) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    __u32 tid = bpf_get_current_pid_tgid();
    __u64 ts = bpf_ktime_get_ns();
    
    __u64 ssl_ptr = (__u64)ssl;
    struct request_info *req = bpf_map_lookup_elem(&tls_context, &ssl_ptr);
    if (!req)
        return 0;
    
    // Read decrypted data
    if (ret > 0 && buf) {
        char data[128];
        __builtin_memset(data, 0, sizeof(data));
        bpf_probe_read_user(data, sizeof(data), buf);
        
        // Check if HTTP
        __u16 status = detect_http_status(data, sizeof(data));
        
        // Send TLS event with decrypted data
        struct tls_event *event = bpf_ringbuf_reserve(&tls_events, sizeof(*event), 0);
        if (event) {
            event->pid = pid;
            event->tid = tid;
            event->timestamp_ns = ts;
            event->direction = DIR_INCOMING;
            event->event_type = EVENT_TLS_DATA;
            event->data_len = ret;
            event->ssl_ptr = ssl_ptr;
            
            bpf_ringbuf_submit(event, 0);
        }
    }
    
    __u64 duration = ts - req->start_time_ns;
    
    // Cleanup
    bpf_map_delete_elem(&tls_context, &ssl_ptr);
    
    return 0;
}

/*
 * SSL_do_handshake - track TLS handshakes
 */
SEC("uprobe/SSL_do_handshake")
int BPF_PROG(ssl_handshake_entry, void *ssl, int ret) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    __u64 ts = bpf_ktime_get_ns();
    
    // Just log handshake for now
    struct tls_event *event = bpf_ringbuf_reserve(&tls_events, sizeof(*event), 0);
    if (event) {
        event->pid = pid;
        event->timestamp_ns = ts;
        event->event_type = EVENT_TLS_DATA;
        bpf_ringbuf_submit(event, 0);
    }
    
    return 0;
}

char _license[] SEC("license") = "GPL";
