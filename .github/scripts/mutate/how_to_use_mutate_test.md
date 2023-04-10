# what is mutatation testing?
Mutation testing (or Mutation analysis or Program mutation) is used to design new software tests and evaluate the quality of existing software tests. Mutation testing involves modifying a program in small ways. Each mutated version is called a mutant and tests detect and reject mutants by causing the behavior of the original version to differ from the mutant. This is called killing the mutant. Test suites are measured by the percentage of mutants that they kill. New tests can be designed to kill additional mutants.

# what is the difference between mutants?
there are several kind of mutants:
1. killed mutants: the mutants which is killed by the unit test. which is identified by "tests passed -> FAIL" in the log
2. failed or escaped mutants: the mutants which pass the unit test. which is identified by "tests failed -> PASS" in the log
3. skipped mutants: the mutants may skipped because of 1. out of coverage code. 2. in the black list, 3. skipped by comment. 
4. other exception cases.
# how to checkout the failed mutants?
1. open the github action workflow page.
2. click "run mutate test" step.
3. search "tests passed " keyword, all the "tests passed -> FAIL" mutants are failed.
you can try here: https://github.com/juicedata/juicefs/actions/runs/3565436367/jobs/5990603552
# how to fix failed mutants?
1. open the github action workflow page.
2. click "run mutate test" step.
3. search "tests passed " keyword, all the "tests passed -> FAIL" mutants are failed.
3. find which line is changed by mutation.
4. copy the changed line to .go source file
5. run all the tests in corresponding go test file, all the tests should passed.
6. you should add test case to make the test failed, which kill this mutant.
# how to add a mutation to black list?
1. find the checksum from the github action log, like FAIL "/tmp/go-mutesting-1324412688/pkg/chunk/prefetch.go.0" with checksum bb9e9497f17e191adf89b5a2ef6764eb
2. add a line //checksum: bb9e9497f17e191adf89b5a2ef6764eb in the go test file.
For example:
//checksum 9cb13bb28aa7918edaf4f0f4ca92eea5
//checksum 05debda2840d31bac0ab5c20c5510591
func TestMin(t *testing.T) {
	assertEqual(t, Min(1, 2), 1)
	assertEqual(t, Min(-1, -2), -2)
	assertEqual(t, Min(0, 0), 0)
}

# how to skip mutate a specific line?
Add "//skip mutate" to the end of the line you don't want to mutate in the source file.
For example:
	if err != nil { //skip mutate
		return "", fmt.Errorf("failed to execute command `lsb_release`: %s", err)
	}

# how to skip a specific test case?
if you don't want to run a specific test case, you can add "//skip mutate" after the test case function.
For example:
func TestRandomWrite(t *testing.T) {//skip mutate
	...
}

# how to customize mutate test job in parallel?
if the mutants of the target source file is more than 200, we will use 4 github jobs to run it. otherwise we will use 1 job to run.
you can customize it in your test file with adding "//mutate_test_job_number: number", eg: //mutate_test_job_number: 8

# how to disable muate test for a specific go file?
add //mutate:disable in the *_test.go file to disable the mutate test.