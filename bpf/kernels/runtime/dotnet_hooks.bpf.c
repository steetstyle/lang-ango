/*
 * Lang-Ango .NET Runtime Hooks
 * Captures method entry/exit for managed .NET code
 */

#include <linux/bpf.h>
#include <linux/ptrace.h>
#include <linux/sched.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>
#include <bpf/bpf_tracing.h>

#define MAX_METHOD_NAME 128
#define MAX_CALL_STACK 32

struct method_entry_event {
    __u32 pid;
    __u32 tid;
    __u64 timestamp_ns;
    
    __u8  event_type;
    __u8  depth;
    
    __u64 method_addr;
    __u64 duration_ns;
    
    __u32 method_name_len;
    char method_name[MAX_METHOD_NAME];
    
    __u32 call_stack_len;
    __u64 call_stack[MAX_CALL_STACK];
};

/* Smaller tracking struct to stay within 512-byte BPF stack limit */
struct method_call_track {
    __u32 pid;
    __u32 tid;
    __u64 timestamp_ns;
    __u64 method_addr;
    __u8  depth;
};

struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, __u64);  // Thread ID
    __type(value, struct method_call_track);
    __uint(max_entries, 8192);
} dotnet_method_stack SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, __u32);  // PID
    __type(value, __u64);  // Method count
    __uint(max_entries, 4096);
} dotnet_method_counts SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    __uint(max_entries, 128 * 1024);
} dotnet_method_events SEC(".maps");

SEC("uprobe/coreclr_MethodTable")
int BPF_PROG(dotnet_method_entry, void *methodTable, void *methodAddr) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    __u32 tid = bpf_get_current_pid_tgid();
    __u64 ts = bpf_ktime_get_ns();

    __u64 key = (__u64)tid;
    __u8 next_depth = 0;

    /* Split null check from field access so the BPF verifier can track
     * the pointer's non-NULL state across both branches. */
    struct method_call_track *pending = bpf_map_lookup_elem(&dotnet_method_stack, &key);
    if (pending) {
        if (pending->depth >= MAX_CALL_STACK)
            return 0;
        next_depth = pending->depth + 1;
    }

    struct method_entry_event *event = bpf_ringbuf_reserve(&dotnet_method_events,
        sizeof(struct method_entry_event), 0);
    if (!event)
        return 0;

    event->pid = pid;
    event->tid = tid;
    event->timestamp_ns = ts;
    event->event_type = 1;
    event->method_addr = (unsigned long)methodAddr;
    event->depth = next_depth;

    __builtin_memset(event->method_name, 0, MAX_METHOD_NAME);
    event->method_name_len = 0;

    bpf_ringbuf_submit(event, 0);

    /* Use the small tracking struct (32 bytes) instead of the full event
     * struct (~436 bytes) to stay within the 512-byte BPF stack limit. */
    struct method_call_track track = {
        .pid        = pid,
        .tid        = tid,
        .timestamp_ns = ts,
        .method_addr  = (unsigned long)methodAddr,
        .depth        = next_depth,
    };
    bpf_map_update_elem(&dotnet_method_stack, &key, &track, BPF_ANY);

    __u64 *count = bpf_map_lookup_elem(&dotnet_method_counts, &pid);
    if (count) {
        (*count)++;
    } else {
        __u64 c = 1;
        bpf_map_update_elem(&dotnet_method_counts, &pid, &c, BPF_ANY);
    }

    return 0;
}

SEC("uprobe/coreclr_PrestubWorker")
int BPF_PROG(dotnet_method_exit, void *method) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    __u32 tid = bpf_get_current_pid_tgid();
    __u64 ts = bpf_ktime_get_ns();

    __u64 key = (__u64)tid;
    struct method_call_track *track = bpf_map_lookup_elem(&dotnet_method_stack, &key);
    if (!track)
        return 0;

    __u64 duration = ts - track->timestamp_ns;
    __u64 method_addr = track->method_addr;
    __u8  depth = track->depth;

    struct method_entry_event *event = bpf_ringbuf_reserve(&dotnet_method_events,
        sizeof(struct method_entry_event), 0);
    if (!event)
        return 0;

    event->pid = pid;
    event->tid = tid;
    event->timestamp_ns = ts;
    event->event_type = 2;
    event->method_addr = method_addr;
    event->duration_ns = duration;
    event->depth = depth;

    bpf_ringbuf_submit(event, 0);

    if (depth > 0) {
        struct method_call_track updated = {
            .pid          = pid,
            .tid          = tid,
            .timestamp_ns = ts,
            .method_addr  = method_addr,
            .depth        = depth - 1,
        };
        bpf_map_update_elem(&dotnet_method_stack, &key, &updated, BPF_ANY);
    } else {
        bpf_map_delete_elem(&dotnet_method_stack, &key);
    }

    return 0;
}

SEC("uprobe/coreclr_ExceptionThrown")
int BPF_PROG(dotnet_exception, void *exceptionObj, void *ip) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    __u32 tid = bpf_get_current_pid_tgid();
    __u64 ts = bpf_ktime_get_ns();
    
    struct method_entry_event *event = bpf_ringbuf_reserve(&dotnet_method_events,
        sizeof(struct method_entry_event), 0);
    if (!event)
        return 0;
    
    event->pid = pid;
    event->tid = tid;
    event->timestamp_ns = ts;
    event->event_type = 3;
    
    __builtin_memset(event->method_name, 0, MAX_METHOD_NAME);
    event->method_name_len = 0;
    
    bpf_ringbuf_submit(event, 0);
    
    return 0;
}

SEC("uprobe/coreclr_ThreadpoolMgr_NotifyThreadCreated")
int BPF_PROG(dotnet_thread_created, void *threadID, void *workerThread) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    __u32 tid = bpf_get_current_pid_tgid();
    __u64 ts = bpf_ktime_get_ns();
    
    struct method_entry_event *event = bpf_ringbuf_reserve(&dotnet_method_events,
        sizeof(struct method_entry_event), 0);
    if (!event)
        return 0;
    
    event->pid = pid;
    event->tid = tid;
    event->timestamp_ns = ts;
    event->event_type = 4;
    
    bpf_ringbuf_submit(event, 0);
    
    return 0;
}

SEC("uprobe/coreclr_ThreadpoolMgr_NotifyThreadDestroyed")
int BPF_PROG(dotnet_thread_destroyed, void *threadID) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    __u64 ts = bpf_ktime_get_ns();
    
    struct method_entry_event *event = bpf_ringbuf_reserve(&dotnet_method_events,
        sizeof(struct method_entry_event), 0);
    if (!event)
        return 0;
    
    event->pid = pid;
    event->timestamp_ns = ts;
    event->event_type = 5;
    
    bpf_ringbuf_submit(event, 0);
    
    return 0;
}

SEC("uprobe/coreclr_GCHeapAlloc")
int BPF_PROG(dotnet_gc_alloc, void *obj, unsigned int size) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    __u64 ts = bpf_ktime_get_ns();
    
    return 0;
}

SEC("uprobe/coreclr_GCHeapCollect")
int BPF_PROG(dotnet_gc_collect, int gen, int blocking) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    __u64 ts = bpf_ktime_get_ns();
    
    struct method_entry_event *event = bpf_ringbuf_reserve(&dotnet_method_events,
        sizeof(struct method_entry_event), 0);
    if (!event)
        return 0;
    
    event->pid = pid;
    event->timestamp_ns = ts;
    event->event_type = 6;
    event->method_addr = gen;
    
    bpf_ringbuf_submit(event, 0);
    
    return 0;
}

char _license[] SEC("license") = "GPL";
