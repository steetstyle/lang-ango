/*
 * Lang-Ango CPU Profiling via Stack Sampling
 * Uses perf event to periodically sample stack traces
 */

#include <linux/bpf.h>
#include <linux/ptrace.h>
#include <linux/perf_event.h>
#include <linux/sched.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>
#include <bpf/bpf_endian.h>

#define MAX_STACK_DEPTH 64
#define MAX_PIDS 4096

struct sample_event {
    __u32 pid;
    __u32 cpu;
    __u64 timestamp_ns;
    __u64 stack_id;
    __u8  stack_depth;
    __u64 addresses[MAX_STACK_DEPTH];
};

struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, __u32);  // PID
    __type(value, __u64);  // Last sample time
    __uint(max_entries, MAX_PIDS);
} pid_filter SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_STACK_TRACE);
    __type(key, __u32);  // Stack ID
    __type(value, __u64[MAX_STACK_DEPTH]);
    __uint(max_entries, 16384);
} stack_traces SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    __uint(max_entries, 256 * 1024);
} cpu_profile_events SEC(".maps");

static __always_inline int get_stack_depth(void *ctx) {
    struct perf_sample_data *data;
    struct pt_regs *regs;
    
    bpf_probe_read(&data, sizeof(data), ctx);
    bpf_probe_read(&regs, sizeof(regs), &data->regs);
    
    if (!regs)
        return 0;
        
    return 0;
}

SEC("perf_event/sched/sched_process_exec")
int BPF_PROG(sched_process_exec, struct perf_event_header *hdr) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    __u64 ts = bpf_ktime_get_ns();
    
    struct sample_event *event = bpf_ringbuf_reserve(&cpu_profile_events,
        sizeof(struct sample_event), 0);
    if (!event)
        return 0;
    
    event->pid = pid;
    event->cpu = bpf_get_smp_processor_id();
    event->timestamp_ns = ts;
    event->event_type = 1;
    
    bpf_ringbuf_submit(event, 0);
    return 0;
}

SEC("fentry/scheduler_tick")
int BPF_PROG(scheduler_tick, struct rq *rq, struct task_struct *curr) {
    __u32 pid = curr->pid;
    __u64 ts = bpf_ktime_get_ns();
    
    __u64 *last = bpf_map_lookup_elem(&pid_filter, &pid);
    if (last && (ts - *last) < 10000000)
        return 0;
    
    struct sample_event *event = bpf_ringbuf_reserve(&cpu_profile_events,
        sizeof(struct sample_event), 0);
    if (!event)
        return 0;
    
    event->pid = pid;
    event->cpu = bpf_get_smp_processor_id();
    event->timestamp_ns = ts;
    
    __builtin_memset(event->addresses, 0, sizeof(event->addresses));
    event->stack_depth = 0;
    
    bpf_ringbuf_submit(event, 0);
    
    __u64 val = ts;
    bpf_map_update_elem(&pid_filter, &pid, &val, BPF_ANY);
    
    return 0;
}

SEC("kprobe/__put_task_struct")
int BPF_PROG(task_exit, struct task_struct *tsk) {
    __u32 pid = tsk->pid;
    bpf_map_delete_elem(&pid_filter, &pid);
    return 0;
}

SEC("kprobe/finish_task_switch")
int BPF_PROG(context_switch, struct task_struct *prev) {
    return 0;
}

char _license[] SEC("license") = "GPL";
