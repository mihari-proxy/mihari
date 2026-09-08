//go:build darwin && (amd64 || arm64)

#include "textflag.h"

TEXT mihari_fgetattrlist_trampoline<>(SB),NOSPLIT,$0-0
 JMP mihari_fgetattrlist(SB)
GLOBL ·libcFgetattrlistAddr(SB), RODATA, $8
DATA ·libcFgetattrlistAddr(SB)/8, $mihari_fgetattrlist_trampoline<>(SB)

TEXT mihari_filesec_init_trampoline<>(SB),NOSPLIT,$0-0
 JMP mihari_filesec_init(SB)
GLOBL ·libcFilesecInitAddr(SB), RODATA, $8
DATA ·libcFilesecInitAddr(SB)/8, $mihari_filesec_init_trampoline<>(SB)

TEXT mihari_filesec_free_trampoline<>(SB),NOSPLIT,$0-0
 JMP mihari_filesec_free(SB)
GLOBL ·libcFilesecFreeAddr(SB), RODATA, $8
DATA ·libcFilesecFreeAddr(SB)/8, $mihari_filesec_free_trampoline<>(SB)

TEXT mihari_filesec_set_trampoline<>(SB),NOSPLIT,$0-0
 JMP mihari_filesec_set_property(SB)
GLOBL ·libcFilesecSetAddr(SB), RODATA, $8
DATA ·libcFilesecSetAddr(SB)/8, $mihari_filesec_set_trampoline<>(SB)

TEXT mihari_fchmodx_trampoline<>(SB),NOSPLIT,$0-0
 JMP mihari_fchmodx_np(SB)
GLOBL ·libcFchmodxAddr(SB), RODATA, $8
DATA ·libcFchmodxAddr(SB)/8, $mihari_fchmodx_trampoline<>(SB)
