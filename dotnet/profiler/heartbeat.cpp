/*
 * Lang-Ango .NET Profiler - Minimal Heartbeat Version
 * Just tests IPC connection to Go Agent
 */

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <errno.h>
#include <time.h>
#include <stdint.h>

#define SOCKET_PATH "/tmp/langango.sock"
#define HEARTBEAT_MSG "LANG_HEARTBEAT"

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

int connectToAgent() {
    int sock = socket(AF_UNIX, SOCK_STREAM, 0);
    if (sock < 0) {
        fprintf(stderr, "[PROFILER] Failed to create socket: %s\n", strerror(errno));
        return -1;
    }
    
    struct sockaddr_un addr;
    memset(&addr, 0, sizeof(addr));
    addr.sun_family = AF_UNIX;
    strncpy(addr.sun_path, SOCKET_PATH, sizeof(addr.sun_path) - 1);
    
    if (connect(sock, (struct sockaddr*)&addr, sizeof(addr)) < 0) {
        fprintf(stderr, "[PROFILER] Failed to connect to Go Agent: %s\n", strerror(errno));
        close(sock);
        return -1;
    }
    
    return sock;
}

int sendHeartbeat(int sock, QWORD seq) {
    IPCHeader header;
    header.magic = 0x4C414E47;  // "LANG"
    header.version = 1;
    header.type = 5;  // Heartbeat
    header.payloadSize = strlen(HEARTBEAT_MSG);
    header.sequence = seq;
    
    char buffer[256];
    memcpy(buffer, &header, sizeof(header));
    strcpy(buffer + sizeof(header), HEARTBEAT_MSG);
    
    ssize_t total = sizeof(header) + header.payloadSize;
    ssize_t sent = send(sock, buffer, total, 0);
    
    if (sent != total) {
        fprintf(stderr, "[PROFILER] Failed to send heartbeat: %s\n", strerror(errno));
        return -1;
    }
    
    return 0;
}

int main(int argc, char* argv[]) {
    printf("[PROFILER] Starting Lang-Ango .NET Profiler (Heartbeat Mode)\n");
    
    QWORD seq = 0;
    while (1) {
        int sock = connectToAgent();
        if (sock >= 0) {
            printf("[PROFILER] Connected to Go Agent, sending heartbeat #%llu\n", (unsigned long long)seq);
            
            if (sendHeartbeat(sock, seq) == 0) {
                printf("[PROFILER] Heartbeat sent successfully!\n");
            }
            
            close(sock);
        } else {
            fprintf(stderr, "[PROFILER] Could not connect, retrying in 5s...\n");
        }
        
        sleep(5);
        seq++;
    }
    
    return 0;
}
