CC ?= gcc
CFLAGS = -Wall -Wextra -O2 -g

TESTS = test_basic test_flags test_odirect

.PHONY: all clean

all: $(TESTS)

test_basic: test_basic.c common.h
	$(CC) $(CFLAGS) -o $@ test_basic.c

test_flags: test_flags.c common.h
	$(CC) $(CFLAGS) -o $@ test_flags.c

test_odirect: test_odirect.c common.h
	$(CC) $(CFLAGS) -o $@ test_odirect.c

clean:
	rm -f $(TESTS)
