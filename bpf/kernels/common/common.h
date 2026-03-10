/* 
 * Lang-Ango eBPF Common Definitions
 * Shared structures and constants for all eBPF programs
 */

#ifndef _LANG_ANGO_COMMON_H
#define _LANG_ANGO_COMMON_H

#define _GNU_SOURCE

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

/* 
 * Event Types 
 */
#define EVENT_UNKNOWN         0
#define EVENT_HTTP_REQUEST    1
#define EVENT_HTTP_RESPONSE   2
#define EVENT_GRPC_REQUEST   3
#define EVENT_GRPC_RESPONSE   4
#define EVENT_DB_QUERY        5
#define EVENT_DB_RESPONSE     6
#define EVENT_TLS_DATA       7
#define EVENT_EXCEPTION       8

/*
 * Protocol Types
 */
#define PROTO_UNKNOWN   0
#define PROTO_HTTP      1
#define PROTO_HTTP2     2
#define PROTO_GRPC      3
#define PROTO_POSTGRES  4
#define PROTO_MYSQL     5
#define PROTO_REDIS     6
#define PROTO_KAFKA     7
#define PROTO_MONGODB   8
#define PROTO_TLS       9

/*
 * Connection Direction
 */
#define DIR_OUTGOING    0
#define DIR_INCOMING    1

/*
 * HTTP Methods
 */
#define HTTP_METHOD_UNKNOWN  0
#define HTTP_METHOD_GET      1
#define HTTP_METHOD_POST     2
#define HTTP_METHOD_PUT       3
#define HTTP_METHOD_DELETE   4
#define HTTP_METHOD_PATCH     5
#define HTTP_METHOD_HEAD     6
#define HTTP_METHOD_OPTIONS  7

/*
 * Maximum sizes
 */
#define MAX_PATH_LEN      256
#define MAX_HOST_LEN      128
#define MAX_HEADER_LEN    512
#define MAX_QUERY_LEN     1024

/*
 * Connection tracking key
 */
struct conn_key {
    __u32 pid;
    __u32 tid;
    __u32 saddr;
    __u32 daddr;
    __u16 sport;
    __u16 dport;
    __u8  protocol;
};

/*
 * Request tracking info
 */
struct request_info {
    __u64 start_time_ns;
    __u32 pid;
    __u32 tid;
    __u32 saddr;
    __u32 daddr;
    __u16 sport;
    __u16 dport;
    __u64 content_length;
    __u8  direction;
    __u8  method;
    __u8  pad[2];
};

/*
 * HTTP Event data
 */
struct http_event {
    __u32 pid;
    __u32 tid;
    __u64 timestamp_ns;
    __u8  direction;
    __u8  protocol;
    __u8  event_type;
    __u8  method;
    
    /* Address info */
    __u32 saddr;
    __u32 daddr;
    __u16 sport;
    __u16 dport;
    
    /* HTTP specific */
    __u16 status;
    __u16 path_len;
    __u64 request_len;
    __u64 response_len;
    __u64 duration_ns;
    
    /* Path buffer */
    char path[MAX_PATH_LEN];
};

/*
 * Database Event
 */
struct db_event {
    __u32 pid;
    __u32 tid;
    __u64 timestamp_ns;
    __u8  direction;
    __u8  protocol;
    __u8  event_type;
    __u8  pad;
    
    __u32 saddr;
    __u32 daddr;
    __u16 sport;
    __u16 dport;
    
    __u16 operation;
    __u16 pad2;
    __u64 duration_ns;
    __u64 query_len;
    
    char query[MAX_QUERY_LEN];
};

/*
 * TLS Event
 */
struct tls_event {
    __u32 pid;
    __u32 tid;
    __u64 timestamp_ns;
    __u8  direction;     // 0=write(encrypt), 1=read(decrypt)
    __u8  event_type;
    __u8  pad[2];
    
    __u32 saddr;
    __u32 daddr;
    __u16 sport;
    __u16 dport;
    
    __u32 data_len;
    __u64 ssl_ptr;
};

/*
 * Aggregated metrics per endpoint
 */
struct endpoint_metrics {
    __u64 request_count;
    __u64 error_count;
    __u64 total_duration_ns;
    __u64 total_bytes_sent;
    __u64 total_bytes_recv;
    __u64 histogram[11];  // Buckets for latency histogram
};

/*
 * Helper: Get port from sockaddr
 */
static __always_inline __u16 get_sport(struct sockaddr *addr) {
    if (!addr)
        return 0;
        
    struct sockaddr_in *addr4 = (struct sockaddr_in *)addr;
    if (addr4->sin_family == AF_INET) {
        return bpf_ntohs(addr4->sin_port);
    }
    
    struct sockaddr_in6 *addr6 = (struct sockaddr_in6 *)addr;
    return bpf_ntohs(addr6->sin6_port);
}

/*
 * Helper: Detect HTTP method
 */
static __always_inline __u8 detect_http_method(const char *data, __u32 len) {
    if (len < 4)
        return HTTP_METHOD_UNKNOWN;
        
    if (data[0] == 'G' && data[1] == 'E' && data[2] == 'T' && data[3] == ' ')
        return HTTP_METHOD_GET;
    if (data[0] == 'P' && data[1] == 'O' && data[2] == 'S' && data[3] == 'T')
        return HTTP_METHOD_POST;
    if (data[0] == 'P' && data[1] == 'U' && data[2] == 'T' && data[3] == ' ')
        return HTTP_METHOD_PUT;
    if (data[0] == 'D' && data[1] == 'E' && data[2] == 'L' && data[3] == 'E')
        return HTTP_METHOD_DELETE;
    if (data[0] == 'P' && data[1] == 'A' && data[2] == 'T' && data[3] == 'C')
        return HTTP_METHOD_PATCH;
    if (data[0] == 'H' && data[1] == 'E' && data[2] == 'A' && data[3] == 'D')
        return HTTP_METHOD_HEAD;
    if (data[0] == 'O' && data[1] == 'P' && data[2] == 'T' && data[3] == 'I')
        return HTTP_METHOD_OPTIONS;
        
    return HTTP_METHOD_UNKNOWN;
}

/*
 * Helper: Detect HTTP status code
 */
static __always_inline __u16 detect_http_status(const char *data, __u32 len) {
    if (len < 12)
        return 0;
        
    if (data[0] == 'H' && data[1] == 'T' && data[2] == 'T' && data[3] == 'P') {
        // HTTP/1.1 200 OK
        if (len >= 12) {
            __u8 hundreds = data[9] - '0';
            __u8 tens = data[10] - '0';
            __u8 ones = data[11] - '0';
            
            if (hundreds >= 1 && hundreds <= 5) {
                return hundreds * 100 + tens * 10 + ones;
            }
        }
    }
    
    return 0;
}

/*
 * Helper: Check if data is HTTP
 */
static __always_inline __u8 is_http_data(const char *data, __u32 len) {
    if (len < 4)
        return 0;
        
    // HTTP methods
    if (data[0] == 'G' || data[0] == 'P' || data[0] == 'D' || 
        data[0] == 'H' || data[0] == 'O')
        return 1;
        
    // HTTP response
    if (data[0] == 'H' && data[1] == 'T' && data[2] == 'T' && data[3] == 'P')
        return 1;
        
    return 0;
}

/*
 * Helper: Extract path from HTTP request
 */
static __always_inline void extract_path(const char *data, __u32 len, char *path, __u16 *path_len) {
    __u16 i = 0;
    
    // Find first space (after method)
    for (i = 0; i < len && i < MAX_PATH_LEN - 1; i++) {
        if (data[i] == ' ') {
            i++;
            break;
        }
    }
    
    // Extract path until next space or HTTP version
    __u16 j = 0;
    for (; i < len && j < MAX_PATH_LEN - 1; i++) {
        if (data[i] == ' ' || data[i] == '\r' || data[i] == '\n')
            break;
        path[j++] = data[i];
    }
    
    path[j] = '\0';
    *path_len = j;
}

#endif /* _LANG_ANGO_COMMON_H */
