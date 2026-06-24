CC ?= gcc
CFLAGS = -Wall -Wextra -O2 -g
LDFLAGS = -luring

TESTS = test_basic_io \
        test_fixed_buffers \
        test_registered_files \
        test_splice \
        test_advanced

.PHONY: all clean run

all: $(TESTS)

test_basic_io: test_basic_io.c common.h
	$(CC) $(CFLAGS) -o $@ test_basic_io.c $(LDFLAGS)

test_fixed_buffers: test_fixed_buffers.c common.h
	$(CC) $(CFLAGS) -o $@ test_fixed_buffers.c $(LDFLAGS)

test_registered_files: test_registered_files.c common.h
	$(CC) $(CFLAGS) -o $@ test_registered_files.c $(LDFLAGS)

test_splice: test_splice.c common.h
	$(CC) $(CFLAGS) -o $@ test_splice.c $(LDFLAGS)

test_advanced: test_advanced.c common.h
	$(CC) $(CFLAGS) -o $@ test_advanced.c $(LDFLAGS)

clean:
	rm -f $(TESTS)

run: all
	@echo "Usage: ./run_tests.sh <test_directory>"
	@echo "  test_directory should be on the filesystem you want to test (e.g., JuiceFS mount point)"
