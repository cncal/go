// +build goexperiment.staticlockranking

package runtime

import (
	"unsafe"
)

// lockRankStruct is embedded in mutex
type lockRankStruct struct {
	// static lock ranking of the lock
	rank lockRank
	// pad field to make sure lockRankStruct is a multiple of 8 bytes, even on
	// 32-bit systems.
	pad int
}

// init checks that the partial order in lockPartialOrder fits within the total
// order determined by the order of the lockRank constants.
func init() {
	for rank, list := range lockPartialOrder {
		for _, entry := range list {
			if entry > lockRank(rank) {
				println("lockPartial order row", lockRank(rank).String(), "entry", entry.String())
				throw("lockPartialOrder table is inconsistent with total lock ranking order")
			}
		}
	}
}

func lockInit(l *mutex, rank lockRank) {
	l.rank = rank
}

func getLockRank(l *mutex) lockRank {
	return l.rank
}

// The following functions are the entry-points to record lock
// operations.
// All of these are nosplit and switch to the system stack immediately
// to avoid stack growths. Since a stack growth could itself have lock
// operations, this prevents re-entrant calls.

// lockWithRank is like lock(l), but allows the caller to specify a lock rank
// when acquiring a non-static lock.
//go:nosplit
func lockWithRank(l *mutex, rank lockRank) {
	if l == &debuglock {
		// debuglock is only used for println/printlock(). Don't do lock rank
		// recording for it, since print/println are used when printing
		// out a lock ordering problem below.
		lock2(l)
		return
	}
	if rank == 0 {
		rank = lockRankLeafRank
	}
	gp := getg()
	// Log the new class.
	systemstack(func() {
		i := gp.m.locksHeldLen
		if i >= len(gp.m.locksHeld) {
			throw("too many locks held concurrently for rank checking")
		}
		gp.m.locksHeld[i].rank = rank
		gp.m.locksHeld[i].lockAddr = uintptr(unsafe.Pointer(l))
		gp.m.locksHeldLen++

		// i is the index of the lock being acquired
		if i > 0 {
			checkRanks(gp, gp.m.locksHeld[i-1].rank, rank)
		}
		lock2(l)
	})
}

func checkRanks(gp *g, prevRank, rank lockRank) {
	rankOK := false
	// If rank < prevRank, then we definitely have a rank error
	if prevRank <= rank {
		if rank == lockRankLeafRank {
			// If new lock is a leaf lock, then the preceding lock can
			// be anything except another leaf lock.
			rankOK = prevRank < lockRankLeafRank
		} else {
			// We've already verified the total lock ranking, but we
			// also enforce the partial ordering specified by
			// lockPartialOrder as well. Two locks with the same rank
			// can only be acquired at the same time if explicitly
			// listed in the lockPartialOrder table.
			list := lockPartialOrder[rank]
			for _, entry := range list {
				if entry == prevRank {
					rankOK = true
					break
				}
			}
		}
	}
	if !rankOK {
		printlock()
		println(gp.m.procid, " ======")
		for j, held := range gp.m.locksHeld[:gp.m.locksHeldLen] {
			println(j, ":", held.rank.String(), held.rank, unsafe.Pointer(gp.m.locksHeld[j].lockAddr))
		}
		throw("lock ordering problem")
	}
}

//go:nosplit
func lockRankRelease(l *mutex) {
	if l == &debuglock {
		// debuglock is only used for print/println. Don't do lock rank
		// recording for it, since print/println are used when printing
		// out a lock ordering problem below.
		unlock2(l)
		return
	}
	gp := getg()
	systemstack(func() {
		found := false
		for i := gp.m.locksHeldLen - 1; i >= 0; i-- {
			if gp.m.locksHeld[i].lockAddr == uintptr(unsafe.Pointer(l)) {
				found = true
				copy(gp.m.locksHeld[i:gp.m.locksHeldLen-1], gp.m.locksHeld[i+1:gp.m.locksHeldLen])
				gp.m.locksHeldLen--
			}
		}
		if !found {
			println(gp.m.procid, ":", l.rank.String(), l.rank, l)
			throw("unlock without matching lock acquire")
		}
		unlock2(l)
	})
}

//go:nosplit
func lockWithRankMayAcquire(l *mutex, rank lockRank) {
	gp := getg()
	if gp.m.locksHeldLen == 0 {
		// No possibilty of lock ordering problem if no other locks held
		return
	}

	systemstack(func() {
		i := gp.m.locksHeldLen
		if i >= len(gp.m.locksHeld) {
			throw("too many locks held concurrently for rank checking")
		}
		// Temporarily add this lock to the locksHeld list, so
		// checkRanks() will print out list, including this lock, if there
		// is a lock ordering problem.
		gp.m.locksHeld[i].rank = rank
		gp.m.locksHeld[i].lockAddr = uintptr(unsafe.Pointer(l))
		gp.m.locksHeldLen++
		checkRanks(gp, gp.m.locksHeld[i-1].rank, rank)
		gp.m.locksHeldLen--
	})
}
