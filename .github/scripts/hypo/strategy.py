import subprocess
try:
    __import__("xattr")
except ImportError:
    subprocess.check_call(["pip", "install", "xattr"])
import xattr
try:
    __import__("hypothesis")
except ImportError:
    subprocess.check_call(["pip", "install", "hypothesis"])
from hypothesis import strategies as st
from string import ascii_lowercase
import time
import os
MIN_DIR_NAME=1
MAX_DIR_NAME=8
MIN_FILE_NAME=1
MAX_FILE_NAME=4
MAX_XATTR_NAME=255+10
MAX_XATTR_VALUE=65535+100
MAX_FILE_SIZE=1024*10
MAX_TRUNCATE_LENGTH=1024*128
MAX_FALLOCATE_LENGTH=1024*128
st_file_name = st.text(alphabet=ascii_lowercase, min_size=MIN_FILE_NAME, max_size=MAX_FILE_NAME)
dir_alphabet = ascii_lowercase + './'
# st_dir_name = st.text(alphabet=dir_alphabet, min_size=MIN_DIR_NAME, max_size=MAX_DIR_NAME)
def valid_dir_name():
    name_part = st.text(alphabet=dir_alphabet, min_size=MIN_DIR_NAME, max_size=MAX_DIR_NAME)
    def is_valid(s:str):
        if s.startswith('/'):
            return False
        if '.' in s and (not s.endswith('/.') or not s.endswith('/..')):
            return False
        return True
    return name_part.filter(is_valid)
# st_entry_name = st.text(min_size=MIN_FILE_NAME, max_size=MAX_FILE_NAME)
#TODO: remove filter when bugfix https://github.com/juicedata/jfs/issues/776
#TODO: use characters instead of ascii_lowercase
st_xattr_name = st.text(alphabet=ascii_lowercase, min_size=1, max_size=MAX_XATTR_NAME).filter(lambda x: '\x00' not in x).map(lambda s: "user." + s)
st_xattr_value = st.binary(min_size=1, max_size=MAX_XATTR_VALUE)
st_xattr_flag = st.sampled_from([0, xattr.XATTR_CREATE, xattr.XATTR_REPLACE])
# st_umask = st.integers(min_value=0o000, max_value=0o777)
st_umask = st.just(0o022)
st_entry_mode = st.integers(min_value=0o000, max_value=0o0777)

# TODO: remove alphabet=ascii_lowercase, 
st_lines = st.lists(st.text(alphabet=ascii_lowercase, min_size=0, max_size=10), min_size=1, max_size=10)
# TODO: remove filter a
st_open_mode = st.sampled_from([ 'x', 'a', 'r', 'w', 'a+', 'r+', 'w+', 'xb', 'ab', 'rb', 'wb', 'a+b', 'r+b', 'w+b'])
st_open_errors = st.sampled_from(['strict', 'ignore', 'replace', 'backslashreplace', 'namereplace'])
st_open_flags = st.lists(st.sampled_from([os.O_RDONLY, os.O_WRONLY, os.O_RDWR, os.O_APPEND, os.O_CREAT, os.O_EXCL, os.O_TRUNC, os.O_SYNC, os.O_DSYNC]), unique=True, min_size=1)
# TODO: add 0 to buffering when bugfix: https://github.com/juicedata/jfs/issues/1359
st_buffering = st.sampled_from([-1, 1, 10, 1024])
st_time = st.integers(min_value=0, max_value=int(time.time()))
st_offset = st.integers(min_value=0, max_value=MAX_FILE_SIZE)
st_length = st.integers(min_value=0, max_value=MAX_FILE_SIZE)
st_truncate_length = st.integers(min_value=0, max_value=MAX_TRUNCATE_LENGTH)
st_fallocate_length = st.integers(min_value=0, max_value=MAX_FALLOCATE_LENGTH)
st_whence = st.sampled_from([os.SEEK_SET, os.SEEK_CUR, os.SEEK_END])

@st.composite
def utf8_byte_arrays(draw, min_size=0, max_size=100):
    text = draw(st.text(min_size=min_size, max_size=max_size))
    return text.encode('utf-8')

@st.composite
def utf16_byte_arrays(draw, min_size=0, max_size=100):
    text = draw(st.text(min_size=min_size, max_size=max_size))
    return text.encode('utf-16')

@st.composite
def ascii_byte_arrays(draw, min_size=0, max_size=100):
    text = draw(st.text(alphabet=st.characters(blacklist_categories=['Cs', 'Cc', 'Co', 'Cn'], max_codepoint=127), min_size=min_size, max_size=max_size))
    return text.encode('ascii')
st_binary = st.binary(min_size=0, max_size=MAX_FILE_SIZE)
st_ascii_lowercase = st.text(alphabet=ascii_lowercase, min_size=0, max_size=MAX_FILE_SIZE) # | st.binary(min_size=0, max_size=MAX_FILE_SIZE)
st_unicode = st.text(alphabet=st.characters(max_codepoint=0x10FFFF), min_size=0, max_size=MAX_FILE_SIZE)

# st_content = st.one_of(utf8_byte_arrays(), utf16_byte_arrays(), ascii_byte_arrays(), st_binary, st_unicode, st_ascii_lowercase)
st_content = st.one_of(utf8_byte_arrays(), ascii_byte_arrays(), st_binary, st_unicode, st_ascii_lowercase)
# st_open_encoding = st.sampled_from(['utf-8', 'utf-16', 'utf-32', 'ascii', 'latin-1'])
st_open_encoding = st.sampled_from(['utf-8', 'ascii', 'latin-1'])
