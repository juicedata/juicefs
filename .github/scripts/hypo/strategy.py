import subprocess
try:
    __import__("hypothesis")
except ImportError:
    subprocess.check_call(["pip", "install", "hypothesis"])
from hypothesis import strategies as st
from string import ascii_lowercase
import time
import os
MAX_CODEPOINT=255
MIN_FILE_NAME=4
MAX_FILE_NAME=4
MAX_XATTR_NAME=255+10
MAX_XATTR_VALUE=65535+100
MAX_FILE_SIZE=1024*10
MAX_TRUNCATE_LENGTH=1024*128
MAX_FALLOCATE_LENGTH=1024*128
st_entry_name = st.text(alphabet=ascii_lowercase, min_size=MIN_FILE_NAME, max_size=MAX_FILE_NAME)
# st_entry_name = st.text(min_size=MIN_FILE_NAME, max_size=MAX_FILE_NAME)
st_content = st.binary(min_size=0, max_size=MAX_FILE_SIZE)
#TODO: remove filter when bugfix https://github.com/juicedata/jfs/issues/776
st_xattr_name = st.text(st.characters(), min_size=1, max_size=MAX_XATTR_NAME).filter(lambda x: '\x00' not in x)
st_xattr_value = st.binary(min_size=1, max_size=MAX_XATTR_VALUE)
st_umask = st.integers(min_value=0o000, max_value=0o777)
st_entry_mode = st.integers(min_value=0o000, max_value=0o7777)
st_open_mode = st.sampled_from(['w', 'x', 'a'])
st_open_flags = st.lists(st.sampled_from([os.O_RDONLY, os.O_WRONLY, os.O_RDWR, os.O_APPEND, os.O_CREAT, os.O_EXCL, os.O_TRUNC, os.O_SYNC, os.O_DSYNC, os.O_RSYNC]), unique=True, min_size=1)
st_time=st.integers(min_value=0, max_value=int(time.time()))
st_offset=st.integers(min_value=0, max_value=MAX_FILE_SIZE)
