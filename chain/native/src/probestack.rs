//! `__rust_probestack` for x86_64.
//!
//! wasmer_vm (inside libwasmvm) calls it from its stack-probe libcall. compiler_builtins only exports
//! it under a mangled name now, so the unmangled symbol wasmer_vm references is not defined. This is
//! compiler_builtins' x86_64 routine unchanged: touch every page of the requested stack allocation in
//! order, so the guard page is hit and not skipped.
#[cfg(target_arch = "x86_64")]
core::arch::global_asm!(
    ".globl __rust_probestack",
    ".type __rust_probestack,@function",
    "__rust_probestack:",
    "pushq %rbp",
    "movq %rsp, %rbp",
    "mov %rax, %r11",
    "cmp $0x1000, %r11",
    "jna 3f",
    "2:",
    "sub $0x1000, %rsp",
    "test %rsp, 8(%rsp)",
    "sub $0x1000, %r11",
    "cmp $0x1000, %r11",
    "ja 2b",
    "3:",
    "sub %r11, %rsp",
    "test %rsp, 8(%rsp)",
    "add %rax, %rsp",
    "leave",
    "ret",
    ".size __rust_probestack, . - __rust_probestack",
    options(att_syntax)
);
