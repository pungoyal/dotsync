package dotsync

import "syscall"

func ctimeNanos(st *syscall.Stat_t) int64 { return st.Ctim.Nano() }
