
#include "openssl.h"

static __always_inline bool openssl_should_trace(__u64 id, void *ssl) {
    // check the pid is monitoring
    __u32 tgid = (__u32)(id >> 32);
    if (tgid_should_trace(tgid) == false) {
        return false;
    }

    // check the socket if server side
    int max_ack_backlog = 0;
    if (0 != bpf_core_read(&max_ack_backlog, sizeof(max_ack_backlog),
                &sock->sk_max_ack_backlog)) {
        return true;
    }
    if (max_ack_backlog == 0) {
        return false;
    }
    return true;
}

int openssl_write(struct pt_regs* ctx) {
    return 0;
}

int openssl_write_ret(struct pt_regs* ctx) {
    return 0;
}

int openssl_read(struct pt_regs* ctx) {
    return 0;
}

int openssl_read_ret(struct pt_regs* ctx) {
    return 0;
}

