# what is mutate test?
Mutation testing (or Mutation analysis or Program mutation) is used to design new software tests and evaluate the quality of existing software tests. Mutation testing involves modifying a program in small ways. Each mutated version is called a mutant and tests detect and reject mutants by causing the behavior of the original version to differ from the mutant. This is called killing the mutant. Test suites are measured by the percentage of mutants that they kill. New tests can be designed to kill additional mutants.

# what is the difference between mutants?
there are several kind of mutants:
1. killed mutants: the mutants which is killed by the unit test. which is identified by "tests passed -> FAIL" in the log
2. failed or escaped mutants: the mutants which pass the unit test. which is identified by "tests failed -> PASS" in the log
3. skipped mutants: the mutants may skipped because of 1. out of coverage code. 2. in the black list, 3. skipped by comment. 4. other exception cases.

# how to checkout the failed mutation?
1. open the github action workflow page
2. click "run mutate test" step
3. search "tests passed " keyword, all the "tests passed -> FAIL" mutants are failed.

# how to fix failed mutants?
1. check which line is changed
2. copy the changed line to your xxx_test.go file
3. run the tests in xxx_test.go, you can find all the tests passed.
4. you should add test case to make the test failed. 

# how to add a mutation to black list?
1. find the checksum from the github action log, like FAIL "/tmp/go-mutesting-1324412688/pkg/chunk/prefetch.go.0" with checksum bb9e9497f17e191adf89b5a2ef6764eb
2. add a line //checksum: bb9e9497f17e191adf89b5a2ef6764eb in the xxx_test.go file.

# how to skip mutate a specific line?
Add "//skip mutate" end of the line 
