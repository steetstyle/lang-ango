/* SPDX-License-Identifier: GPL-2.0 */
/* Minimal stub for eBPF compilation - inet_sock/sock types used via kprobes */
#ifndef _BPF_NET_SOCK_H
#define _BPF_NET_SOCK_H

#include <linux/types.h>
#include <linux/in.h>
#include <linux/posix_types.h>

struct iovec {
    void            *iov_base;
    __kernel_size_t  iov_len;
};

struct msghdr {
    void            *msg_name;
    int              msg_namelen;
    int              msg_inq;
    struct iovec    *msg_iov;
    __kernel_size_t  msg_iovlen;
    void            *msg_control;
    __kernel_size_t  msg_controllen;
    unsigned int     msg_flags;
};

struct socket;

/*
 * Minimal sock_common / sock / inet_sock definitions for eBPF compilation.
 * Field layout mirrors Linux 5.15+ x86_64 for direct-access BPF programs.
 */

struct sock_common {
    union {
        struct {
            __be32 skc_daddr;
            __be32 skc_rcv_saddr;
        };
    };
    union {
        unsigned int skc_hash;
        __u16 skc_u16hashes[2];
    };
    union {
        struct {
            __be16 skc_dport;
            __u16  skc_num;
        };
    };
    short           skc_family;
    volatile signed char skc_state;
    unsigned char   skc_reuse:4;
    unsigned char   skc_reuseport:1;
    unsigned char   skc_ipv6only:1;
    unsigned char   skc_net_refcnt:1;
    int             skc_bound_dev_if;
    /* padding to approximate real sock_common size */
    unsigned char   _pad[196];
};

struct sock {
    struct sock_common __sk_common;
    /* approximate padding to real struct sock size (~880 bytes on x86_64) */
    unsigned char   _pad[656];
};

struct inet_sock {
    struct sock     sk;
    __be32          inet_saddr;
    __s16           uc_ttl;
    __u16           cmsg_flags;
    __be16          inet_sport;
    __u16           inet_id;
    __u8            tos;
    __u8            min_ttl;
    __u8            mc_ttl;
    __u8            pmtudisc;
    __u8            recverr:1;
    __u8            is_icsk:1;
    __u8            freebind:1;
    __u8            hdrincl:1;
    __u8            mc_loop:1;
    __u8            transparent:1;
    __u8            mc_all:1;
    __u8            nodefrag:1;
    __u8            bind_address_no_port:1;
    __u8            recverr_rfc4884:1;
    __u8            defer_connect:1;
    __u8            _pad8[1];
    __u8            rcv_tos;
    __u8            convert_csum;
    int             uc_index;
    int             mc_index;
    __be32          mc_addr;
    __be32          inet_daddr;
    int             inet_num;
    __be16          inet_dport;
};

#endif /* _BPF_NET_SOCK_H */
