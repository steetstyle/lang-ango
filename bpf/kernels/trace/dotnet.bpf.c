/*
 * Lang-Ango eBPF .NET CoreCLR Exception Tracer
 * Captures .NET exceptions via CoreCLR internals
 */

#include <linux/bpf.h>
#include <linux/ptrace.h>
#include <linux/sched.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>

/*
 * Exception event structure
 */
struct dotnet_exception_event {
    __u32 pid;
    __u32 tid;
    __u64 timestamp_ns;
    
    /* Exception details */
    __u32 exception_type_len;
    __u32 message_len;
    
    /* Stack trace info */
    __u32 stack_trace_len;
    
    /* Raw data buffer */
    char data[256];
};

/*
 * Map for tracking .NET exceptions
 */
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, __u64);  // PID + TID
    __type(value, struct dotnet_exception_event);
    __uint(max_entries, 4096);
} dotnet_exceptions SEC(".maps");

/*
 * Ring buffer for exception events
 */
struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    __uint(max_entries, 64 * 1024);
} exception_events SEC(".maps");

/*
 * Hook into libcoreclr.so - ExceptionThrown
 * This is triggered when a .NET exception is thrown
 */
SEC("uprobe/coreclr_ExceptionThrown")
int BPF_PROG(coreclr_exception_thrown, void *exceptionObj, void *ip) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    __u32 tid = bpf_get_current_pid_tgid();
    __u64 ts = bpf_ktime_get_ns();
    
    struct dotnet_exception_event *event = bpf_ringbuf_reserve(&exception_events, 
        sizeof(struct dotnet_exception_event), 0);
    if (!event)
        return 0;
    
    event->pid = pid;
    event->tid = tid;
    event->timestamp_ns = ts;
    
    // Try to read exception object details
    // Note: This requires reading from managed heap memory
    // The actual implementation would need to read through the 
    // object reference to get type name and message
    
    bpf_ringbuf_submit(event, 0);
    
    return 0;
}

/*
 * Hook into libcoreclr.so - IL_Throw
 * Called when IL code throws an exception
 */
SEC("uprobe/coreclr_IL_Throw")
int BPF_PROG(coreclr_il_throw, void *throwable, void *ip) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    __u32 tid = bpf_get_current_pid_tgid();
    __u64 ts = bpf_ktime_get_ns();
    
    struct dotnet_exception_event *event = bpf_ringbuf_reserve(&exception_events,
        sizeof(struct dotnet_exception_event), 0);
    if (!event)
        return 0;
    
    event->pid = pid;
    event->tid = tid;
    event->timestamp_ns = ts;
    
    // Capture IP where exception was thrown
    __builtin_memcpy(event->data, &ip, sizeof(void*));
    
    bpf_ringbuf_submit(event, 0);
    
    return 0;
}

/*
 * Hook into libcoreclr.so - Exception welfare
 * Called during exception handling
 */
SEC("uprobe/coreclr_ExceptionWelfare")
int BPF_PROG(coreclr_exception_welfare, void *exception, int code) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    
    // Track exception lifecycle
    __u64 key = ((__u64)pid << 32) | code;
    
    struct dotnet_exception_event evt = {
        .pid = pid,
        .timestamp_ns = bpf_ktime_get_ns(),
    };
    
    bpf_map_update_elem(&dotnet_exceptions, &key, &evt, BPF_ANY);
    
    return 0;
}

/*
 * GC Hook - track garbage collection events
 * Useful for performance correlation
 */
SEC("uprobe/coreclr_GCHeapCollect")
int BPF_PROG(coreclr_gc_collect, int gen, int blocking) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    
    struct dotnet_exception_event *event = bpf_ringbuf_reserve(&exception_events,
        sizeof(struct dotnet_exception_event), 0);
    if (!event)
        return 0;
    
    event->pid = pid;
    event->timestamp_ns = bpf_ktime_get_ns();
    event->exception_type_len = gen;  // Reuse field for GC gen
    
    bpf_ringbuf_submit(event, 0);
    
    return 0;
}

/*
 * Thread creation tracking
 */
SEC("uprobe/coreclr_ThreadCreated")
int BPF_PROG(coreclr_thread_created, void *thread, void *tid) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    
    return 0;
}

char _license[] SEC("license") = "GPL";
