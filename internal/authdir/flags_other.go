//go:build !unix

package authdir

// nonBlockFlag is unnecessary where FIFOs do not exist (Windows).
const nonBlockFlag = 0
