package cmdline

import (
	"testing"
)

// ---------------------------------------------------------------------------

func TestEscape(t *testing.T) {

	for i := 0; i < escTableBaseChar; i++ {
		checkEscapeChar(t, i, i)
	}

	table := make([]int, escTableLen)
	for i := 0; i < escTableLen; i++ {
		table[i] = escTableBaseChar + i
	}
	table['0'-escTableBaseChar] = 0
	table['r'-escTableBaseChar] = '\r'
	table['t'-escTableBaseChar] = '\t'
	table['n'-escTableBaseChar] = '\n'
	for i := 0; i < escTableLen; i++ {
		checkEscapeChar(t, escTableBaseChar+i, table[i])
	}

	for i := int(escTableBaseChar + escTableLen); i < 256; i++ {
		checkEscapeChar(t, i, i)
	}
}

func checkEscapeChar(t *testing.T, i, exp int) {

	ret := defaultEscape(byte(i))
	if ret != string(exp) {
		t.Fatal("escapeChar failed:", i)
	}
}

// ---------------------------------------------------------------------------

