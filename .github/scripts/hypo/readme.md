1. format juicefs with trash day of 0. 
   ./juicefs format sqlite3://test.db myjfs
2. mount juicefs with xatrr enable.
   ./juicefs mount sqlite3://test.db /tmp/jfs --enable-xattr
3. run the test.
   python3 .github/scripts/hypo/fs.py
4. run the test with custom examples and step count to reach deep bugs.
   MAX_EXAMPLE=1000 STEP_COUNT=500 .github/scripts/hypo/fs.py
5. you can modify EXCLUDE_RULES to skip running some operations.