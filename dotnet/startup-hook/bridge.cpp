/*
 * LangAngo Bridge - Production-Ready with Auto-Reconnect & Drop Policy
 */

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <time.h>
#include <stdint.h>
#include <pthread.h>
#include <errno.h>
#include <atomic>
#include <vector>

#define SOCKET_PATH "/tmp/langango.sock"
#define COMMAND_SOCKET_PATH "/tmp/langango_cmd.sock"

// Runtime-configurable socket path
static char g_custom_socket_path[256] = {0};

// Set custom socket path (called from .NET before init) - extern C to prevent name mangling
extern "C" {
    void langango_bridge_set_socket_path(const char* path) {
        if (path && strlen(path) < 250) {
            strncpy(g_custom_socket_path, path, 255);
            printf("[BRIDGE] Custom socket path set to: %s\n", path);
        }
    }
}

static const char* get_socket_path() {
    return g_custom_socket_path[0] ? g_custom_socket_path : SOCKET_PATH;
}

#define CMD_TYPE_SET_FILTER    1
#define CMD_TYPE_START_STACK  2
#define CMD_TYPE_STOP_STACK   3
#define CMD_TYPE_HEARTBEAT    4
#define CMD_TYPE_SYMBOL_UPDATE 6  // Must match Go IPCTypeSymbolUpdate

typedef uint8_t BYTE;
typedef uint16_t WORD;
typedef uint32_t DWORD;
typedef uint64_t QWORD;

#pragma pack(push, 1)
typedef struct {
    DWORD magic;
    WORD version;
    BYTE type;
    DWORD payloadSize;
    QWORD sequence;
} IPCHeader;
#pragma pack(pop)

// Ring buffer with drop policy
#define RING_BUFFER_SIZE 1024

struct RingItem {
    bool valid;
    BYTE type;
    DWORD payloadSize;
    BYTE payload[4096];
};

static RingItem g_ring_buffer[RING_BUFFER_SIZE];
static std::atomic<int> g_ring_head(0);
static std::atomic<int> g_ring_tail(0);
static pthread_mutex_t g_ring_mutex = PTHREAD_MUTEX_INITIALIZER;

// Statistics
static std::atomic<uint64_t> g_stats_spans_sent(0);
static std::atomic<uint64_t> g_stats_spans_dropped(0);
static std::atomic<uint64_t> g_stats_reconnects(0);

// Connection state
static std::atomic<int> g_connected(0);
static time_t g_last_heartbeat = 0;

// Sampling configuration
static struct {
    int enabled;
    int slow_threshold_ms;
    int capture_stack;
    char endpoint_filter[256];
    pthread_mutex_t mutex;
} g_config = {
    .enabled = 1,
    .slow_threshold_ms = 2000,
    .capture_stack = 1,  // Default to enabled for testing
    .endpoint_filter = {0},
    .mutex = PTHREAD_MUTEX_INITIALIZER
};

static int g_socket = -1;
static QWORD g_sequence = 0;
static pthread_mutex_t g_socket_mutex = PTHREAD_MUTEX_INITIALIZER;
static std::atomic<int> g_running(1);

// Callback for config changes
static void (*g_on_config_change)(int slow_threshold, int capture_stack, const char* filter) = NULL;

// Connect with auto-reconnect
static int connect_to_server() {
    pthread_mutex_lock(&g_socket_mutex);
    
    if (g_socket >= 0) {
        close(g_socket);
        g_socket = -1;
    }
    
    g_socket = socket(AF_UNIX, SOCK_STREAM, 0);
    if (g_socket < 0) {
        printf("[BRIDGE] Failed to create socket\n");
        pthread_mutex_unlock(&g_socket_mutex);
        return -1;
    }
    
    struct sockaddr_un addr;
    memset(&addr, 0, sizeof(addr));
    addr.sun_family = AF_UNIX;
    strncpy(addr.sun_path, get_socket_path(), sizeof(addr.sun_path) - 1);
    
    printf("[BRIDGE] Connecting to %s...\n", get_socket_path());
    if (connect(g_socket, (struct sockaddr*)&addr, sizeof(addr)) < 0) {
        printf("[BRIDGE] Connection failed: %s\n", strerror(errno));
        close(g_socket);
        g_socket = -1;
        g_connected.store(0);
        pthread_mutex_unlock(&g_socket_mutex);
        return -1;
    }
    
    printf("[BRIDGE] Connected to Go agent!\n");
    g_connected.store(1);
    g_stats_reconnects++;
    pthread_mutex_unlock(&g_socket_mutex);
    
    return 0;
}

// Ring buffer functions - NON-BLOCKING with DROP policy
static int ring_push(BYTE type, const BYTE* data, DWORD size) {
    if (size > sizeof(g_ring_buffer[0].payload)) {
        return -1;
    }
    
    int head = g_ring_head.load();
    int next_head = (head + 1) % RING_BUFFER_SIZE;
    
    if (next_head == g_ring_tail.load()) {
        // Buffer full - DROP this span (production-safe)
        g_stats_spans_dropped++;
        return -1;
    }
    
    pthread_mutex_lock(&g_ring_mutex);
    g_ring_buffer[head].valid = true;
    g_ring_buffer[head].type = type;
    g_ring_buffer[head].payloadSize = size;
    if (data && size > 0) {
        memcpy(g_ring_buffer[head].payload, data, size);
    }
    g_ring_head.store(next_head);
    pthread_mutex_unlock(&g_ring_mutex);
    
    printf("[BRIDGE] Pushed span to ring buffer, type=%d, size=%d\n", type, size);
    fflush(stdout);
    
    return 0;
}

static int ring_pop(RingItem* item) {
    int tail = g_ring_tail.load();
    
    if (tail == g_ring_head.load()) {
        return -1;
    }
    
    pthread_mutex_lock(&g_ring_mutex);
    if (!g_ring_buffer[tail].valid) {
        pthread_mutex_unlock(&g_ring_mutex);
        return -1;
    }
    
    item->valid = g_ring_buffer[tail].valid;
    item->type = g_ring_buffer[tail].type;
    item->payloadSize = g_ring_buffer[tail].payloadSize;
    memcpy(item->payload, g_ring_buffer[tail].payload, item->payloadSize);
    g_ring_buffer[tail].valid = false;
    g_ring_tail.store((tail + 1) % RING_BUFFER_SIZE);
    pthread_mutex_unlock(&g_ring_mutex);
    
    return 0;
}

// Flush thread with auto-reconnect
static void* flush_thread(void* arg) {
    fprintf(stderr, "[BRIDGE-DEBUG] Flush thread function entered\n");
    printf("[BRIDGE] Flush thread started, entering loop...\n"); fflush(stdout);
    fprintf(stderr, "[BRIDGE-DEBUG] After first printf\n");
    printf("[BRIDGE] g_running=%d, g_connected=%d\n", g_running.load(), g_connected.load()); fflush(stdout);
    fprintf(stderr, "[BRIDGE-DEBUG] After second printf\n");
    
    int reconnect_delay = 1; // Start with 1 second
    
    while (g_running.load()) {
        // Loop iteration
        
        // Check connection
        if (g_connected.load() == 0) {
            printf("[BRIDGE] Not connected, attempting connection...\n"); fflush(stdout);
            if (connect_to_server() < 0) {
                printf("[BRIDGE] Connection failed, retrying in %ds\n", reconnect_delay); fflush(stdout);
                sleep(reconnect_delay);
                reconnect_delay = std::min(reconnect_delay * 2, 30); // Max 30 seconds
                continue;
            }
        }
        // Connected, processing
        reconnect_delay = 1; // Reset on success
        
        // Try to send heartbeat occasionally
        time_t now = time(NULL);
        if (now - g_last_heartbeat > 10) {
            g_last_heartbeat = now;
            // Heartbeat would be sent here
        }
        
        // Flush ring buffer
        RingItem item;
        while (ring_pop(&item) == 0) {
            pthread_mutex_lock(&g_socket_mutex);
            if (g_socket >= 0) {
                IPCHeader header;
                header.magic = 0x4C414E47;
                header.version = 1;
                header.type = item.type;
                header.payloadSize = item.payloadSize;
                header.sequence = g_sequence++;
                
                char packet[sizeof(header) + sizeof(item.payload)];
                memcpy(packet, &header, sizeof(header));
                memcpy(packet + sizeof(header), item.payload, item.payloadSize);
                
                ssize_t sent = send(g_socket, packet, sizeof(header) + item.payloadSize, 0);
                if (sent > 0) {
                    g_stats_spans_sent++;
                } else {
                    // Connection lost
                    close(g_socket);
                    g_socket = -1;
                    g_connected.store(0);
                }
            }
            pthread_mutex_unlock(&g_socket_mutex);
        }
        
        usleep(1000); // 1ms
    }
    
    printf("[BRIDGE] Flush thread stopped\n");
    printf("[BRIDGE] Stats: sent=%lu, dropped=%lu, reconnects=%lu\n",
           g_stats_spans_sent.load(), g_stats_spans_dropped.load(), g_stats_reconnects.load());
    return NULL;
}

// Command listener thread
static void* command_listener_thread(void* arg) {
    printf("[BRIDGE] Command listener started\n");
    
    int cmd_listen = socket(AF_UNIX, SOCK_STREAM, 0);
    if (cmd_listen < 0) {
        return NULL;
    }
    
    struct sockaddr_un cmd_addr;
    memset(&cmd_addr, 0, sizeof(cmd_addr));
    cmd_addr.sun_family = AF_UNIX;
    unlink(COMMAND_SOCKET_PATH);
    strncpy(cmd_addr.sun_path, COMMAND_SOCKET_PATH, sizeof(cmd_addr.sun_path) - 1);
    
    if (bind(cmd_listen, (struct sockaddr*)&cmd_addr, sizeof(cmd_addr)) < 0) {
        close(cmd_listen);
        return NULL;
    }
    
    listen(cmd_listen, 5);
    printf("[BRIDGE] Command socket listening on %s\n", COMMAND_SOCKET_PATH);
    
    while (g_running.load()) {
        struct sockaddr_un client_addr;
        socklen_t client_len = sizeof(client_addr);
        
        int client = accept(cmd_listen, (struct sockaddr*)&client_addr, &client_len);
        if (client < 0) {
            if (g_running.load()) usleep(100000);
            continue;
        }
        
        char header_buf[sizeof(IPCHeader)];
        ssize_t n = recv(client, header_buf, sizeof(header_buf), 0);
        
        if (n == sizeof(IPCHeader)) {
            IPCHeader* hdr = (IPCHeader*)header_buf;
            
            if (hdr->magic == 0x4C414E47 && hdr->payloadSize > 0 && hdr->payloadSize < 256) {
                char payload[256];
                recv(client, payload, hdr->payloadSize, 0);
                
                if (hdr->type == CMD_TYPE_SET_FILTER) {
                    int slow_threshold = *(int*)payload;
                    int capture_stack = payload[4];
                    char filter[256] = {0};
                    if (hdr->payloadSize > 5) {
                        strncpy(filter, payload + 5, sizeof(filter) - 1);
                    }
                    
                    pthread_mutex_lock(&g_config.mutex);
                    g_config.slow_threshold_ms = slow_threshold;
                    g_config.capture_stack = capture_stack;
                    strncpy(g_config.endpoint_filter, filter, sizeof(g_config.endpoint_filter) - 1);
                    pthread_mutex_unlock(&g_config.mutex);
                    
                    printf("[BRIDGE] Filter updated: threshold=%dms, stack=%d, filter=%s\n",
                           slow_threshold, capture_stack, filter);
                    
                    if (g_on_config_change) {
                        g_on_config_change(slow_threshold, capture_stack, filter);
                    }
                }
                
                if (hdr->type == CMD_TYPE_SYMBOL_UPDATE && hdr->payloadSize >= 12) {
                    // Symbol update: 8 bytes address + variable length name
                    QWORD address = *(QWORD*)payload;
                    char* name = payload + 8;
                    printf("[BRIDGE] Symbol update: addr=%llx, name=%s\n", (unsigned long long)address, name);
                }
            }
        }
        
        close(client);
        close(client);
    }
    
    close(cmd_listen);
    unlink(COMMAND_SOCKET_PATH);
    return NULL;
}

extern "C" {

void langango_bridge_set_config_callback(void (*cb)(int, int, const char*)) {
    g_on_config_change = cb;
}

void langango_bridge_set_filter(const char* endpoint, int slow_threshold_ms) {
    pthread_mutex_lock(&g_config.mutex);
    if (endpoint) {
        strncpy(g_config.endpoint_filter, endpoint, sizeof(g_config.endpoint_filter) - 1);
    }
    g_config.slow_threshold_ms = slow_threshold_ms;
    pthread_mutex_unlock(&g_config.mutex);
}

int langango_bridge_should_capture_stack() {
    pthread_mutex_lock(&g_config.mutex);
    int result = g_config.capture_stack;
    pthread_mutex_unlock(&g_config.mutex);
    return result;
}

int langango_bridge_is_enabled() {
    pthread_mutex_lock(&g_config.mutex);
    int result = g_config.enabled;
    pthread_mutex_unlock(&g_config.mutex);
    return result;
}

const char* langango_bridge_get_filter() {
    pthread_mutex_lock(&g_config.mutex);
    const char* result = g_config.endpoint_filter;
    pthread_mutex_unlock(&g_config.mutex);
    return result;
}

// Get statistics
uint64_t langango_bridge_get_sent() { return g_stats_spans_sent.load(); }
uint64_t langango_bridge_get_dropped() { return g_stats_spans_dropped.load(); }

void langango_bridge_init(const char* socketPath) {
    printf("[BRIDGE] Initializing bridge (production mode)\n");
    fflush(stdout);
    
    // Try to connect IMMEDIATELY (before thread creation)
    printf("[BRIDGE] Attempting initial connection to Go agent...\n");
    fflush(stdout);
    if (connect_to_server() == 0) {
        printf("[BRIDGE] Initial connection successful!\n");
        fflush(stdout);
    } else {
        printf("[BRIDGE] Initial connection failed, will retry in thread\n");
        fflush(stdout);
    }
    
    // Start background threads
    static pthread_t flush_tid, cmd_tid;
    int rc1 = pthread_create(&flush_tid, NULL, flush_thread, NULL);
    int rc2 = pthread_create(&cmd_tid, NULL, command_listener_thread, NULL);
    
    printf("[BRIDGE] pthread_create results: flush_thread=%d, cmd_thread=%d\n", rc1, rc2);
    fflush(stdout);
}

void langango_bridge_send_span(
    const char* traceId,
    const char* spanId,
    const char* parentSpanId,
    const char* operationName,
    long long startTimeNs,
    long long endTimeNs,
    int threadId
) {
    // Build payload - DROP if buffer full (non-blocking)
    char payload[512];
    int offset = 0;
    
    memset(payload + offset, 0, 32);
    if (traceId) strncpy(payload + offset, traceId, 32);
    offset += 32;
    
    memset(payload + offset, 0, 16);
    if (spanId) strncpy(payload + offset, spanId, 16);
    offset += 16;
    
    memset(payload + offset, 0, 16);
    if (parentSpanId) strncpy(payload + offset, parentSpanId, 16);
    offset += 16;
    
    memset(payload + offset, 0, 256);
    if (operationName) strncpy(payload + offset, operationName, 255);
    offset += 256;
    
    memcpy(payload + offset, &startTimeNs, 8);
    offset += 8;
    memcpy(payload + offset, &endTimeNs, 8);
    offset += 8;
    memcpy(payload + offset, &threadId, 4);
    offset += 4;
    
    DWORD frameCount = 0;
    memcpy(payload + offset, &frameCount, 4);
    offset += 4;
    
    // Push to ring buffer - NON-BLOCKING
    ring_push(1, (BYTE*)payload, offset);
}

// Send span with stack frames and names (up to 16 frames)
void langango_bridge_send_span_with_stack(
    const char* traceId,
    const char* spanId,
    const char* parentSpanId,
    const char* operationName,
    long long startTimeNs,
    long long endTimeNs,
    int threadId,
    void* stackFrames,
    int frameCount,
    const char* frameNames
) {
    printf("[BRIDGE] send_span_with_stack called: frames=%d, traceId=%.8s...\n", frameCount, traceId ? traceId : "null");
    fflush(stdout);
    
    char payload[4096];
    int offset = 0;
    
    memset(payload + offset, 0, 32);
    if (traceId) strncpy(payload + offset, traceId, 32);
    offset += 32;
    
    memset(payload + offset, 0, 16);
    if (spanId) strncpy(payload + offset, spanId, 16);
    offset += 16;
    
    memset(payload + offset, 0, 16);
    if (parentSpanId) strncpy(payload + offset, parentSpanId, 16);
    offset += 16;
    
    memset(payload + offset, 0, 256);
    if (operationName) strncpy(payload + offset, operationName, 255);
    offset += 256;
    
    memcpy(payload + offset, &startTimeNs, 8);
    offset += 8;
    memcpy(payload + offset, &endTimeNs, 8);
    offset += 8;
    memcpy(payload + offset, &threadId, 4);
    offset += 4;
    
    int actualFrames = frameCount > 16 ? 16 : frameCount;
    DWORD fc = (DWORD)actualFrames;
    memcpy(payload + offset, &fc, 4);
    offset += 4;
    
    if (stackFrames && actualFrames > 0) {
        memcpy(payload + offset, stackFrames, actualFrames * 8);
        offset += actualFrames * 8;
    }
    
    // Append frame names (pipe-separated strings)
    if (frameNames && actualFrames > 0) {
        int namesLen = strlen(frameNames);
        if (namesLen > 2048) namesLen = 2048;
        memcpy(payload + offset, frameNames, namesLen);
        payload[offset + namesLen] = '\0';
        offset += namesLen + 1;
    }
    
    printf("[BRIDGE] send_span_with_stack: total offset=%d\n", offset);
    fflush(stdout);
    
    int result = ring_push(1, (BYTE*)payload, offset);
    printf("[BRIDGE] ring_push result=%d\n", result);
    fflush(stdout);
}

void langango_bridge_send_exception(
    const char* traceId,
    const char* exceptionType,
    const char* message
) {
    char payload[512];
    int offset = 0;
    
    memset(payload + offset, 0, 32);
    if (traceId) strncpy(payload + offset, traceId, 32);
    offset += 32;
    
    memset(payload + offset, 0, 128);
    if (exceptionType) strncpy(payload + offset, exceptionType, 127);
    offset += 128;
    
    memset(payload + offset, 0, 256);
    if (message) strncpy(payload + offset, message, 255);
    offset += 256;
    
    QWORD timestamp = (QWORD)time(NULL) * 1000000000ULL;
    memcpy(payload + offset, &timestamp, 8);
    offset += 8;
    
    ring_push(4, (BYTE*)payload, offset);
}

// Send symbol update (address -> name mapping) to Go agent
void langango_bridge_send_symbol(unsigned long long address, const char* methodName) {
    char payload[512];
    int offset = 0;
    
    // 8 bytes for address
    memcpy(payload + offset, &address, 8);
    offset += 8;
    
    // Method name (variable length, null-terminated)
    memset(payload + offset, 0, 500);
    if (methodName) strncpy(payload + offset, methodName, 499);
    offset += (int)strlen(methodName) + 1;
    
    printf("[BRIDGE] Sending symbol: addr=%llx, name=%s\n", address, methodName);
    
    // Push to ring buffer with type 6 (symbol update) - matches Go IPCTypeSymbolUpdate
    ring_push(CMD_TYPE_SYMBOL_UPDATE, (BYTE*)payload, offset);
}

void langango_bridge_shutdown() {
    g_running.store(0);
    pthread_mutex_lock(&g_socket_mutex);
    if (g_socket >= 0) {
        close(g_socket);
        g_socket = -1;
    }
    pthread_mutex_unlock(&g_socket_mutex);
}

} // extern "C"
