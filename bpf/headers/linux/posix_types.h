/* SPDX-License-Identifier: GPL-2.0 WITH Linux-syscall-note */
/* Minimal stub for eBPF compilation */
#ifndef _LINUX_POSIX_TYPES_H
#define _LINUX_POSIX_TYPES_H

#include <linux/stddef.h>

typedef long            __kernel_long_t;
typedef unsigned long   __kernel_ulong_t;
typedef __kernel_ulong_t __kernel_size_t;
typedef __kernel_long_t  __kernel_ssize_t;
typedef int             __kernel_pid_t;
typedef unsigned int    __kernel_uid_t;
typedef unsigned int    __kernel_gid_t;
typedef __kernel_long_t  __kernel_off_t;
typedef long long       __kernel_loff_t;
typedef unsigned short  __kernel_sa_family_t;

#ifndef _SIZE_T
#define _SIZE_T
typedef __SIZE_TYPE__ size_t;
#endif

#endif /* _LINUX_POSIX_TYPES_H */
