/*
 * Mock Span Client - Simulates .NET Profiler sending spans
 * Tests the full IPC pipeline: C++ → Unix Socket → Go → OTLP → Jaeger
 */

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <time.h>
#include <stdint.h>
#include <random>
#include <pthread.h>

#define SOCKET_PATH "/tmp/langango.sock"

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

const char* methodNames[] = {
    "UserController.GetUser",
    "UserService.ValidateToken",
    "UserRepository.FindById",
    "Database.Query",
    "HttpClient.GetAsync",
    "Cache.Get",
    "AuthService.Login",
    "ValidationService.Validate"
};

const char* classNames[] = {
    "Controllers",
    "Services",
    "Repositories",
    "Middleware",
    "Filters"
};

int connectToAgent() {
    int sock = socket(AF_UNIX, SOCK_STREAM, 0);
    if (sock < 0) return -1;
    
    struct sockaddr_un addr;
    memset(&addr, 0, sizeof(addr));
    addr.sun_family = AF_UNIX;
    strncpy(addr.sun_path, SOCKET_PATH, sizeof(addr.sun_path) - 1);
    
    if (connect(sock, (struct sockaddr*)&addr, sizeof(addr)) < 0) {
        close(sock);
        return -1;
    }
    
    return sock;
}

void generateTraceId(char* buf) {
    static std::mt19937_64 rng(std::random_device{}());
    static const char hex[] = "0123456789abcdef";
    for (int i = 0; i < 32; i++) {
        buf[i] = hex[rng() % 16];
    }
    buf[32] = '\0';
}

void generateSpanId(char* buf) {
    static std::mt19937_64 rng(std::random_device{}());
    static const char hex[] = "0123456789abcdef";
    for (int i = 0; i < 16; i++) {
        buf[i] = hex[rng() % 16];
    }
    buf[16] = '\0';
}

int sendSpanWithStack(int sock, QWORD seq) {
    // Simulate: UserController.GetUser -> UserService.ValidateToken -> Database.Query
    const char* traceId = "0123456789abcdef0123456789abcdef";
    const char* spanId = "abcdef0123456789";
    const char* parentSpanId = "1234567890abcdef";
    const char* operationName = "GET /api/users/{id}";
    
    // Build payload
    char payload[1024];
    int offset = 0;
    
    // traceId (32 bytes)
    memcpy(payload + offset, traceId, 32);
    offset += 32;
    
    // spanId (16 bytes)
    memcpy(payload + offset, spanId, 16);
    offset += 16;
    
    // parentSpanId (16 bytes)
    memcpy(payload + offset, parentSpanId, 16);
    offset += 16;
    
    // operationName (256 bytes, null-padded)
    memset(payload + offset, 0, 256);
    strncpy(payload + offset, operationName, 255);
    offset += 256;
    
    // startTimeNS (8 bytes)
    QWORD startTime = (QWORD)time(NULL) * 1000000000ULL;
    memcpy(payload + offset, &startTime, 8);
    offset += 8;
    
    // endTimeNS (8 bytes)
    QWORD endTime = startTime + 150000000ULL; // 150ms
    memcpy(payload + offset, &endTime, 8);
    offset += 8;
    
    // threadId (4 bytes)
    DWORD threadId = pthread_self() % 10000;
    memcpy(payload + offset, &threadId, 4);
    offset += 4;
    
    // stackFrameCount (4 bytes) - simulate 3 frames
    DWORD frameCount = 3;
    memcpy(payload + offset, &frameCount, 4);
    offset += 4;
    
    // Stack frames (simplified - just IP addresses)
    // Frame 1: UserController.GetUser (0x7ffee1234000)
    QWORD ip1 = 0x7ffee1234000 + (rand() % 0x1000);
    memcpy(payload + offset, &ip1, 8);
    offset += 8;
    
    // Frame 2: UserService.ValidateToken (0x7ffee4567000)
    QWORD ip2 = 0x7ffee4567000 + (rand() % 0x1000);
    memcpy(payload + offset, &ip2, 8);
    offset += 8;
    
    // Frame 3: Database.Query (0x7ffee89ab000)
    QWORD ip3 = 0x7ffee89ab000 + (rand() % 0x1000);
    memcpy(payload + offset, &ip3, 8);
    offset += 8;
    
    // Send header + payload
    IPCHeader header;
    header.magic = 0x4C414E47;  // "LANG"
    header.version = 1;
    header.type = 1;  // SpanWithStack
    header.payloadSize = offset;
    header.sequence = seq;
    
    char packet[sizeof(header) + sizeof(payload)];
    memcpy(packet, &header, sizeof(header));
    memcpy(packet + sizeof(header), payload, offset);
    
    ssize_t total = sizeof(header) + offset;
    ssize_t sent = send(sock, packet, total, 0);
    
    if (sent != total) {
        fprintf(stderr, "[MOCK] Failed to send span: %zd/%zd\n", sent, total);
        return -1;
    }
    
    printf("[MOCK] Sent span: traceId=%s, op=%s, frames=%d\n", 
           traceId, operationName, frameCount);
    return 0;
}

int sendThreadSample(int sock, QWORD seq) {
    char payload[256];
    int offset = 0;
    
    // traceId
    char traceId[33];
    generateTraceId(traceId);
    memcpy(payload + offset, traceId, 32);
    offset += 32;
    
    // osThreadId
    DWORD osThreadId = pthread_self() % 10000;
    memcpy(payload + offset, &osThreadId, 4);
    offset += 4;
    
    // stackFrameCount
    DWORD frameCount = 5;
    memcpy(payload + offset, &frameCount, 4);
    offset += 4;
    
    // timestamp
    QWORD timestamp = (QWORD)time(NULL) * 1000000000ULL;
    memcpy(payload + offset, &timestamp, 8);
    offset += 8;
    
    // Stack frames (just IPs)
    for (int i = 0; i < 5; i++) {
        QWORD ip = 0x7ffee0001000 + (rand() % 0x10000);
        memcpy(payload + offset, &ip, 8);
        offset += 8;
    }
    
    IPCHeader header;
    header.magic = 0x4C414E47;
    header.version = 1;
    header.type = 3;  // ThreadSample
    header.payloadSize = offset;
    header.sequence = seq;
    
    char packet[sizeof(header) + sizeof(payload)];
    memcpy(packet, &header, sizeof(header));
    memcpy(packet + sizeof(header), payload, offset);
    
    ssize_t total = sizeof(header) + offset;
    send(sock, packet, total, 0);
    
    printf("[MOCK] Sent thread sample: threadId=%u, frames=%d\n", osThreadId, frameCount);
    return 0;
}

int sendException(int sock, QWORD seq) {
    char payload[512];
    int offset = 0;
    
    // traceId
    char traceId[33];
    generateTraceId(traceId);
    memcpy(payload + offset, traceId, 32);
    offset += 32;
    
    // exceptionType (128 bytes)
    memset(payload + offset, 0, 128);
    strncpy(payload + offset, "System.InvalidOperationException", 127);
    offset += 128;
    
    // message (256 bytes)
    memset(payload + offset, 0, 256);
    strncpy(payload + offset, "User not found in database", 255);
    offset += 256;
    
    // timestamp
    QWORD timestamp = (QWORD)time(NULL) * 1000000000ULL;
    memcpy(payload + offset, &timestamp, 8);
    offset += 8;
    
    IPCHeader header;
    header.magic = 0x4C414E47;
    header.version = 1;
    header.type = 4;  // Exception
    header.payloadSize = offset;
    header.sequence = seq;
    
    char packet[sizeof(header) + sizeof(payload)];
    memcpy(packet, &header, sizeof(header));
    memcpy(packet + sizeof(header), payload, offset);
    
    send(sock, packet, sizeof(header) + offset, 0);
    
    printf("[MOCK] Sent exception: %s\n", "System.InvalidOperationException");
    return 0;
}

int main(int argc, char* argv[]) {
    printf("[MOCK] Starting Mock Span Client\n");
    printf("[MOCK] Connecting to %s\n", SOCKET_PATH);
    
    srand(time(NULL));
    
    QWORD seq = 0;
    while (1) {
        int sock = connectToAgent();
        if (sock < 0) {
            fprintf(stderr, "[MOCK] Failed to connect, retrying in 2s...\n");
            sleep(2);
            continue;
        }
        
        printf("[MOCK] Connected, sending mock spans...\n");
        
        // Send different types of data
        sendSpanWithStack(sock, seq++);
        usleep(100000); // 100ms
        
        sendThreadSample(sock, seq++);
        usleep(100000);
        
        // Occasionally send exception
        if (rand() % 5 == 0) {
            sendException(sock, seq++);
        }
        
        close(sock);
        
        printf("[MOCK] Sleeping 3s before next batch...\n");
        sleep(3);
    }
    
    return 0;
}
