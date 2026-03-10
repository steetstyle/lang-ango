/*
 * Lang-Ango Python Exception Tracer
 * Captures Python exceptions via CPython internals
 */

#include <linux/bpf.h>
#include <linux/ptrace.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>

/*
 * Python exception event
 */
struct python_exception_event {
    __u32 pid;
    __u32 tid;
    __u64 timestamp_ns;
    __u32 type_len;
    __u32 value_len;
};

/*
 * Ring buffer for Python exceptions
 */
struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    __uint(max_entries, 64 * 1024);
} python_events SEC(".maps");

/*
 * Hook: PyErr_SetString
 * Called when Python raises an exception
 */
SEC("uprobe/PyErr_SetString")
int BPF_PROG(python_err_setstring, void *exceptionType, void *message) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    __u32 tid = bpf_get_current_pid_tgid();
    __u64 ts = bpf_ktime_get_ns();
    
    struct python_exception_event *event = bpf_ringbuf_reserve(&python_events,
        sizeof(struct python_exception_event), 0);
    if (!event)
        return 0;
    
    event->pid = pid;
    event->tid = tid;
    event->timestamp_ns = ts;
    
    bpf_ringbuf_submit(event, 0);
    
    return 0;
}

/*
 * Hook: PyErr_SetObject
 * Called when raising an exception with object
 */
SEC("uprobe/PyErr_SetObject")
int BPF_PROG(python_err_setobject, void *exceptionType, void *value) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    __u64 ts = bpf_ktime_get_ns();
    
    struct python_exception_event *event = bpf_ringbuf_reserve(&python_events,
        sizeof(struct python_exception_event), 0);
    if (!event)
        return 0;
    
    event->pid = pid;
    event->timestamp_ns = ts;
    
    bpf_ringbuf_submit(event, 0);
    
    return 0;
}

/*
 * Hook: PyTraceBack_Print
 * Called when printing traceback
 */
SEC("uprobe/PyTraceBack_Print")
int BPF_PROG(python_traceback_print, void *obj, void *file, int flags) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    __u64 ts = bpf_ktime_get_ns();
    
    struct python_exception_event *event = bpf_ringbuf_reserve(&python_events,
        sizeof(struct python_exception_event), 0);
    if (!event)
        return 0;
    
    event->pid = pid;
    event->timestamp_ns = ts;
    
    bpf_ringbuf_submit(event, 0);
    
    return 0;
}

/*
 * Hook: PyEval_EvalFrameEx
 * Main Python bytecode interpreter - for GC events
 */
SEC("uprobe/PyEval_EvalFrameEx")
int BPF_PROG(python_eval_frame, void *frame, int throwflag) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    
    return 0;
}

char _license[] SEC("license") = "GPL";
