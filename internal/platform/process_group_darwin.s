//go:build darwin && (amd64 || arm64)

#include "textflag.h"

TEXT mihari_proc_listpids_trampoline<>(SB),NOSPLIT,$0-0
 JMP mihari_proc_listpids(SB)
GLOBL ·libcProcListPIDsAddr(SB), RODATA, $8
DATA ·libcProcListPIDsAddr(SB)/8, $mihari_proc_listpids_trampoline<>(SB)
