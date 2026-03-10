/*
 * Lang-Ango .NET Runtime Hooks
 * Captures method entry/exit for managed .NET code
 */

#include <linux/bpf.h>
#include <linux/ptrace.h>
#include <linux/sched.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>

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

struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, __u64);  // Thread ID
    __type(value, struct method_entry_event);
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
    
    struct method_entry_event *pending = bpf_map_lookup_elem(&dotnet_method_stack, &key);
    if (pending && pending->depth >= MAX_CALL_STACK)
        return 0;
    
    struct method_entry_event *event = bpf_ringbuf_reserve(&dotnet_method_events,
        sizeof(struct method_entry_event), 0);
    if (!event)
        return 0;
    
    event->pid = pid;
    event->tid = tid;
    event->timestamp_ns = ts;
    event->event_type = 1;
    event->method_addr = (unsigned long)methodAddr;
    event->depth = pending ? pending->depth + 1 : 0;
    
    __builtin_memset(event->method_name, 0, MAX_METHOD_NAME);
    event->method_name_len = 0;
    
    bpf_ringbuf_submit(event, 0);
    
    struct method_entry_event entry = {
        .pid = pid,
        .tid = tid,
        .timestamp_ns = ts,
        .event_type = 1,
        .method_addr = (unsigned long)methodAddr,
        .depth = event->depth,
    };
    bpf_map_update_elem(&dotnet_method_stack, &key, &entry, BPF_ANY);
    
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
    struct method_entry_event *entry = bpf_map_lookup_elem(&dotnet_method_stack, &key);
    if (!entry)
        return 0;
    
    __u64 duration = ts - entry->timestamp_ns;
    
    struct method_entry_event *event = bpf_ringbuf_reserve(&dotnet_method_events,
        sizeof(struct method_entry_event), 0);
    if (!event)
        return 0;
    
    event->pid = pid;
    event->tid = tid;
    event->timestamp_ns = ts;
    event->event_type = 2;
    event->method_addr = entry->method_addr;
    event->duration_ns = duration;
    event->depth = entry->depth;
    
    bpf_ringbuf_submit(event, 0);
    
    if (entry->depth > 0) {
        entry->timestamp_ns = ts;
        bpf_map_update_elem(&dotnet_method_stack, &key, entry, BPF_ANY);
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
