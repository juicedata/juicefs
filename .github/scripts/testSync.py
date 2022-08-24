import subprocess
import random
import shutil
from hypothesis import given, strategies as st, settings, example
import os

JFS_SOURCE_DIR='/Users/chengzhou/Documents/juicefs/pkg/'
JFS_SOURCE_DIR='jfs_source/pkg/'
MOUNT_POINT='/tmp/sync-test/'
JFS_BIN='./juicefs-1.0.0-beta1'
JFS_BIN='./juicefs-1.0.0-beta2'
JFS_BIN='./juicefs-1.0.0-beta3'
JFS_BIN='./juicefs'
MAX_EXAMPLE=100

def setup():
    meta_url = 'sqlite3://abc.db'
    volume_name='sync-test'
    if os.path.isfile('abc.db'):
        os.remove('abc.db')
    if os.path.exists(MOUNT_POINT):
        os.system('umount %s'%MOUNT_POINT)
    cache_dir = os.path.expanduser('~/.juicefs/local/%s/'%volume_name)
    if os.path.exists(cache_dir):
        try:
            shutil.rmtree(cache_dir)
        except OSError as e:
            print("Error: %s : %s" % (cache_dir, e.strerror))
    subprocess.check_call([JFS_BIN, 'format', meta_url, volume_name])
    subprocess.check_call([JFS_BIN, 'mount', '-d', meta_url, MOUNT_POINT])
    subprocess.check_call([JFS_BIN, 'sync', JFS_SOURCE_DIR, MOUNT_POINT+'jfs_source/'])

def generate_all_entries(root_dir):
    entries = set()
    for root, dirs, files in os.walk(root_dir):
        # print(root)
        for d in dirs:
            entries.add(d+'/')
        for file in files:
            entries.add(file)
            file_path = os.path.join(root, file)[len(root_dir):]
            entries.add(file_path)
    print(len(entries))
    return entries

def generate_nested_dir(root_dir):
    result = []
    for root, dirs, files in os.walk(root_dir):
        for d in dirs:
            dir = os.path.join(root, d)[len(root_dir):]
            li = dir.split('/')
            entries = []
            for i in range(0, len(li)):
                entries.append('/'.join(li[i:])+'/')
            for i in range(0, len(entries)):
                result.append(random.sample(entries, random.randint(0, min(len(entries), 5)) ))
    print(result)
    return result

def change_entry(entries):
    # entries = random.sample( entries, random.randint(0, min(len(entries), 5)) )
    options = []
    for entry in entries:
        type = random.choice(['--include', '--exclude'])
        value = entry.replace(random.choice(entry), random.choice(['*', '?']), random.randint(0,2))
        # print(type+' '+value)
        options.append( (type, "'%s'"%value) )
    # print(options)
    return options

all_entry = generate_all_entries(JFS_SOURCE_DIR)
st_all_entry = st.lists(st.sampled_from(list(all_entry))).map(lambda x: change_entry(x)).filter(lambda x: len(x) != 0)
nested_dir = generate_nested_dir(JFS_SOURCE_DIR)
st_nested_dir = st.sampled_from(nested_dir).map(lambda x: change_entry(x)).filter(lambda x: len(x) != 0)
valid_name = st.text(st.characters(max_codepoint=1000, blacklist_categories=('Cc', 'Cs')), min_size=2).map(lambda s: s.strip()).filter(lambda s: len(s) > 0)
st_random_text = st.lists(valid_name).map(lambda x: change_entry(x)).filter(lambda x: len(x) != 0)

@given(sync_options=st_random_text)
@example([['--include', '[*'] ])
@settings(max_examples=MAX_EXAMPLE, deadline=None)
def test_sync_with_random_text(sync_options):
    print(sync_options)
    compare_rsync_and_juicesync(sync_options)

@given(sync_options=st_all_entry)
@settings(max_examples=MAX_EXAMPLE, deadline=None)
def test_sync_with_path_entry(sync_options):
    compare_rsync_and_juicesync(sync_options)

@given(sync_options=st_nested_dir)
@example([ ['--include', 'chu*/'],  ['--exclude', 'pk*/'],  ['--exclude', '*.go'] ])
@settings(max_examples=MAX_EXAMPLE, deadline=None)
def test_sync_with_nested_dir(sync_options):
    compare_rsync_and_juicesync(sync_options)

def compare_rsync_and_juicesync(sync_options):
    assert sync_options != 0
    sync_options = [item for sublist in sync_options for item in sublist]
    do_rsync(MOUNT_POINT+'jfs_source/', 'rsync_dir/', sync_options)
    do_juicesync(MOUNT_POINT+'jfs_source/', 'juicesync_dir/', sync_options)
    diff_result = os.system('diff -ur juicesync_dir rsync_dir')
    assert diff_result==0

def do_juicesync(source_dir, dest_dir, sync_options):
    if os.path.exists(dest_dir):
        shutil.rmtree(dest_dir)
    os.makedirs(dest_dir)
    juicesync_cmd = [JFS_BIN , 'sync', '--dirs', source_dir, dest_dir]+sync_options
    print('juicesync_cmd: '+' '.join(juicesync_cmd))
    try:
        subprocess.check_call(juicesync_cmd)
    except Exception as e:
        assert False

def do_rsync(source_dir, dest_dir, sync_options):
    if os.path.exists(dest_dir):
        shutil.rmtree(dest_dir)
    os.makedirs(dest_dir)
    rsync_cmd = ['rsync', '-a', '-r' , source_dir,  dest_dir]+sync_options
    print('rsync_cmd: '+ ' '.join(rsync_cmd))
    try:
        subprocess.check_call(rsync_cmd)
    except Exception as e:
        assert False

if __name__ == "__main__":
    setup()
    test_sync_with_random_text()
    test_sync_with_nested_dir()
    test_sync_with_path_entry()
    
