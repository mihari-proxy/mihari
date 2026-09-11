//go:build darwin && (amd64 || arm64)

#include "textflag.h"

TEXT mihari_service_sysctl_trampoline<>(SB),NOSPLIT,$0-0
 JMP mihari_service_sysctl(SB)
GLOBL ·libcServiceSysctlAddr(SB), RODATA, $8
DATA ·libcServiceSysctlAddr(SB)/8, $mihari_service_sysctl_trampoline<>(SB)
