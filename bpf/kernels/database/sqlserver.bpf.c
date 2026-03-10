/*
 * Lang-Ango eBPF SQL Server (TDS) Protocol Tracer
 * Captures SQL Server, Azure SQL queries via TDS protocol
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

/*
 * TDS Protocol Constants
 */
#define TDS_PACKET_TYPE_SQL_BATCH      0x01
#define TDS_PACKET_TYPE_RPC            0x03
#define TDS_PACKET_TYPE_TDS7_LOGIN    0x10
#define TDS_PACKET_TYPE_RESPONSE       0x04
#define TDS_PACKET_TYPE_BULK_LOAD     0x07
#define TDS_PACKET_TYPE_TRANSACTION_MGR 0x0E

#define TDS_LOGIN7_PACKET_SIZE        94
#define TDS_MAX_PACKET_LENGTH        32767

/*
 * TDS Packet Header
 */
struct tds_packet {
    __u8  packet_type;
    __u8  status;
    __be16 length;
    __u16 spid;
    __u8  packet_id;
    __u8  window;
};

/*
 * SQL Server connection tracking
 */
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, __u64);  // PID + FD
    __type(value, struct request_info);
    __uint(max_entries, 8192);
} tds_connections SEC(".maps");

/*
 * TDS Events Ring Buffer
 */
struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    __uint(max_entries, 128 * 1024);
} tds_events SEC(".maps");

/*
 * Extract SQL query from TDS packet
 * Returns pointer to query start and length
 */
static __always_inline int tds_extract_query(const char *data, __u32 len, 
                                            char *query_buf, __u16 *query_len) {
    if (len < 8)
        return 0;
    
    struct tds_packet *hdr = (struct tds_packet *)data;
    
    // Check if this is a SQL batch packet
    if (hdr->packet_type != TDS_PACKET_TYPE_SQL_BATCH)
        return 0;
    
    __u16 total_len = bpf_ntohs(hdr->length);
    if (total_len < 8 || total_len > len)
        return 0;
    
    // TDS 7.x+: Skip 8-byte header to get to payload
    const char *payload = data + 8;
    __u32 payload_len = total_len - 8;
    
    // Check for Unicode or ANSI string
    // TDS strings can be either:
    // - Single-byte (length-prefixed)
    // - Double-byte Unicode (length in bytes)
    
    // Simple detection: look for common SQL keywords
    __u16 i = 0;
    while (i < payload_len - 4 && i < 200) {
        // Check for common SQL verbs
        if (payload[i] == 'S' && payload[i+1] == 'E' && 
            payload[i+2] == 'L' && payload[i+3] == 'E' && payload[i+4] == 'C') {
            // Found SELECT
            *query_len = payload_len - i;
            if (*query_len > 512)
                *query_len = 512;
            __builtin_memcpy(query_buf, payload + i, *query_len);
            return 1;
        }
        if (payload[i] == 'I' && payload[i+1] == 'N' && 
            payload[i+2] == 'S' && payload[i+3] == 'E' && payload[i+4] == 'R') {
            // Found INSERT
            *query_len = payload_len - i;
            if (*query_len > 512)
                *query_len = 512;
            __builtin_memcpy(query_buf, payload + i, *query_len);
            return 1;
        }
        if (payload[i] == 'U' && payload[i+1] == 'P' && 
            payload[i+2] == 'D' && payload[i+3] == 'A' && payload[i+4] == 'T') {
            // Found UPDATE
            *query_len = payload_len - i;
            if (*query_len > 512)
                *query_len = 512;
            __builtin_memcpy(query_buf, payload + i, *query_len);
            return 1;
        }
        if (payload[i] == 'D' && payload[i+1] == 'E' && 
            payload[i+2] == 'L' && payload[i+3] == 'E' && payload[i+4] == 'T') {
            // Found DELETE
            *query_len = payload_len - i;
            if (*query_len > 512)
                *query_len = 512;
            __builtin_memcpy(query_buf, payload + i, *query_len);
            return 1;
        }
        if (payload[i] == 'E' && payload[i+1] == 'X' && 
            payload[i+2] == 'E' && payload[i+3] == 'C') {
            // Found EXEC
            *query_len = payload_len - i;
            if (*query_len > 512)
                *query_len = 512;
            __builtin_memcpy(query_buf, payload + i, *query_len);
            return 1;
        }
        
        i++;
    }
    
    return 0;
}

/*
 * TCP sendmsg for SQL Server
 */
SEC("kprobe/tcp_sendmsg")
int BPF_PROG(tcp_sendmsg_tds, struct sock *sk, struct msghdr *msg, size_t size) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    __u32 tid = bpf_get_current_pid_tgid();
    __u64 ts = bpf_ktime_get_ns();
    
    struct inet_sock *isk = (struct inet_sock *)sk;
    
    // Check if this is likely SQL Server port (1433)
    __u16 dport = bpf_ntohs(isk->inet_dport);
    __u16 sport = bpf_ntohs(isk->inet_sport);
    
    // Only track port 1433 (SQL Server default)
    if (dport != 1433 && sport != 1433)
        return 0;
    
    // Read I/O vector
    struct iovec *iov = msg->msg_iov;
    if (!iov || msg->msg_iovlen == 0)
        return 0;
    
    char buf[512];
    __builtin_memset(buf, 0, sizeof(buf));
    bpf_probe_read_user(buf, sizeof(buf), iov->iov_base);
    
    // Check if TDS packet
    struct tds_packet *pkt = (struct tds_packet *)buf;
    
    // Validate TDS packet
    if (pkt->packet_type != TDS_PACKET_TYPE_SQL_BATCH &&
        pkt->packet_type != TDS_PACKET_TYPE_RPC)
        return 0;
    
    // Extract SQL query
    char query[256];
    __u16 query_len = 0;
    
    if (tds_extract_query(buf, size, query, &query_len)) {
        // Send event
        struct db_event *event = bpf_ringbuf_reserve(&tds_events, sizeof(*event), 0);
        if (event) {
            event->pid = pid;
            event->tid = tid;
            event->timestamp_ns = ts;
            event->direction = DIR_OUTGOING;
            event->protocol = PROTO_MYSQL; // Use MySQL as proxy, will be identified as MSSQL
            event->event_type = EVENT_DB_QUERY;
            event->saddr = isk->inet_saddr;
            event->daddr = isk->inet_daddr;
            event->sport = bpf_ntohs(sport);
            event->dport = bpf_ntohs(dport);
            event->duration_ns = 0;
            
            if (query_len > 0) {
                __builtin_memcpy(event->query, query, query_len > 255 ? 255 : query_len);
                event->query_len = query_len;
            }
            
            bpf_ringbuf_submit(event, 0);
        }
        
        // Track connection for response
        struct request_info req = {
            .start_time_ns = ts,
            .pid = pid,
            .tid = tid,
            .content_length = size,
            .direction = DIR_OUTGOING,
        };
        
        __u64 key = ((__u64)pid << 32) | (__u64)dport;
        bpf_map_update_elem(&tds_connections, &key, &req, BPF_ANY);
    }
    
    return 0;
}

/*
 * TCP recvmsg for SQL Server responses
 */
SEC("kprobe/tcp_recvmsg")
int BPF_PROG(tcp_recvmsg_tds, struct sock *sk, struct msghdr *msg,
             size_t size, int flags) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    __u64 ts = bpf_ktime_get_ns();
    
    struct inet_sock *isk = (struct inet_sock *)sk;
    __u16 dport = bpf_ntohs(isk->inet_dport);
    __u16 sport = bpf_ntohs(isk->inet_sport);
    
    if (dport != 1433 && sport != 1433)
        return 0;
    
    // Look up the request
    __u64 key = ((__u64)pid << 32) | (__u64)((sport == 1433) ? dport : sport);
    struct request_info *req = bpf_map_lookup_elem(&tds_connections, &key);
    
    if (req) {
        __u64 duration = ts - req->start_time_ns;
        
        // Send response event
        struct db_event *event = bpf_ringbuf_reserve(&tds_events, sizeof(*event), 0);
        if (event) {
            event->pid = pid;
            event->timestamp_ns = ts;
            event->direction = DIR_INCOMING;
            event->protocol = PROTO_MYSQL; // Placeholder
            event->event_type = EVENT_DB_RESPONSE;
            event->duration_ns = duration;
            event->saddr = isk->inet_daddr;
            event->daddr = isk->inet_saddr;
            event->sport = sport;
            event->dport = dport;
            
            bpf_ringbuf_submit(event, 0);
        }
        
        bpf_map_delete_elem(&tds_connections, &key);
    }
    
    return 0;
}

char _license[] SEC("license") = "GPL";
