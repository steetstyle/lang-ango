/*
 * Lang-Ango Go Exception Tracer
 * Captures Go panics and exceptions
 */

#include <linux/bpf.h>
#include <linux/ptrace.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>
#include <bpf/bpf_tracing.h>

/*
 * Go exception event
 */
struct go_exception_event {
    __u32 pid;
    __u32 tid;
    __u64 timestamp_ns;
    __u64 pc;  // Program counter
    __u64 sp;  // Stack pointer
};

/*
 * Map for Go panic tracking
 */
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, __u32);  // PID
    __type(value, struct go_exception_event);
    __uint(max_entries, 4096);
} go_panics SEC(".maps");

/*
 * Ring buffer for Go exceptions
 */
struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    __uint(max_entries, 64 * 1024);
} go_events SEC(".maps");

/*
 * Hook: runtime.gopanic
 * Triggered when Go code panics
 */
SEC("uprobe/go_panic")
int BPF_PROG(go_panic_entry, void *panicArg) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    __u32 tid = bpf_get_current_pid_tgid();
    __u64 ts = bpf_ktime_get_ns();
    
    struct go_exception_event *event = bpf_ringbuf_reserve(&go_events, 
        sizeof(struct go_exception_event), 0);
    if (!event)
        return 0;
    
    event->pid = pid;
    event->tid = tid;
    event->timestamp_ns = ts;
    
    bpf_ringbuf_submit(event, 0);
    
    return 0;
}

/*
 * Hook: runtime.gorecover
 * Triggered when recover() is called
 */
SEC("uprobe/go_recover")
int BPF_PROG(go_recover_entry, void *recoverArg) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    
    // Remove from panic tracking
    bpf_map_delete_elem(&go_panics, &pid);
    
    return 0;
}

/*
 * Hook: runtime.throw
 * Called for runtime errors
 */
SEC("uprobe/go_throw")
int BPF_PROG(go_throw_entry, void *s) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    __u64 ts = bpf_ktime_get_ns();
    
    struct go_exception_event *event = bpf_ringbuf_reserve(&go_events,
        sizeof(struct go_exception_event), 0);
    if (!event)
        return 0;
    
    event->pid = pid;
    event->timestamp_ns = ts;
    
    bpf_ringbuf_submit(event, 0);
    
    return 0;
}

/*
 * Hook: runtime.panicmem
 * Called on invalid memory access
 */
SEC("uprobe/go_panicmem")
int BPF_PROG(go_panicmem_entry, void *s) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    __u64 ts = bpf_ktime_get_ns();
    
    struct go_exception_event *event = bpf_ringbuf_reserve(&go_events,
        sizeof(struct go_exception_event), 0);
    if (!event)
        return 0;
    
    event->pid = pid;
    event->timestamp_ns = ts;
    
    bpf_ringbuf_submit(event, 0);
    
    return 0;
}

/*
 * Hook: runtime.fatalpanic
 * Fatal panic that cannot be recovered
 */
SEC("uprobe/go_fatalpanic")
int BPF_PROG(go_fatalpanic_entry, void *s) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    __u64 ts = bpf_ktime_get_ns();
    
    struct go_exception_event *event = bpf_ringbuf_reserve(&go_events,
        sizeof(struct go_exception_event), 0);
    if (!event)
        return 0;
    
    event->pid = pid;
    event->timestamp_ns = ts;
    
    bpf_ringbuf_submit(event, 0);
    
    return 0;
}

char _license[] SEC("license") = "GPL";
