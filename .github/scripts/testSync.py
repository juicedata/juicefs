import subprocess
import random
import shutil
from hypothesis import given, strategies as st, settings, example, assume
from hypothesis.strategies import composite, tuples
import os

jfs_source_dir=os.path.expanduser('~/Documents/juicefs2/pkg/')
jfs_source_dir='jfs_source/pkg/'

def setup():
    meta_url = "sqlite3://abc.db"
    mount_point='/tmp/sync-test/'
    volume_name='sync-test'
    if os.path.isfile('abc.db'):
        os.remove('abc.db')
    cache_dir = os.path.expanduser('~/.juicefs/local/%s/'%volume_name)
    if os.path.exists(cache_dir):
        try:
            shutil.rmtree(cache_dir)
        except OSError as e:
            print("Error: %s : %s" % (cache_dir, e.strerror))
    
    os.system('./juicefs format %s %s'%(meta_url, volume_name))
    os.system('./juicefs mount --no-usage-report %s %s -d'%(meta_url, mount_point))
    os.system('./juicefs sync %s %s'%(jfs_source_dir, mount_point) )

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

def change_entry(entries):
    li = []
    for entry in entries:
        type = random.choice(['--include', '--exclude'])
        # print(entry)
        value = entry.replace(random.choice(entry), random.choice(['*', '?']), 1)
        # print(value)
        li.append( (type, "'%s'"%value) )
    return li

def generate_nested_dir(root_dir):
    result = []
    for root, dirs, files in os.walk(root_dir):
        for d in dirs:
            dir = os.path.join(root, d)
            li = dir.split('/')
            s = set()
            for i in range(0, len(li)):
                s.add('/'.join(li[i:]))
            result.append(list(s))
    return result

all_entry = generate_all_entries(jfs_source_dir)
st_all_entry = st.lists(st.sampled_from(list(all_entry))).map(lambda x: change_entry(x))
nested_dir = generate_nested_dir(jfs_source_dir)
st_nested_dir = st.sampled_from(nested_dir).map(lambda x: change_entry(x))
valid_name = st.text(st.characters(max_codepoint=1000, blacklist_categories=('Cc', 'Cs')), min_size=2).map(lambda s: s.strip()).filter(lambda s: len(s) > 0)
st_random_text = st.lists(valid_name).map(lambda x: change_entry(x))

@given(sync_options=st_random_text)
@settings(max_examples=10000, deadline=None)
def test_sync_with_random_text(sync_options):
    compare_rsync_and_juicesync(sync_options)

@given(sync_options=st_all_entry)
@settings(max_examples=10000, deadline=None)
def test_sync_with_path_entry(sync_options):
    compare_rsync_and_juicesync(sync_options)

@given(sync_options=st_nested_dir)
@settings(max_examples=10000, deadline=None)
def test_sync_with_nested_dir(sync_options):
    compare_rsync_and_juicesync(sync_options)

@given(options=st_random_text)
@settings(max_examples=100, deadline=None)
def test_idempotent_for_juicesync(sync_options):
    assert len(sync_options) != 0
    sync_options = [item for sublist in sync_options for item in sublist]
    if os.path.exists('juicesync_dir1/'):
        shutil.rmtree('juicesync_dir1/')
    os.makedirs('juicesync_dir1')
    if os.path.exists('juicesync_dir2/'):
        shutil.rmtree('juicesync_dir2/')
    os.makedirs('juicesync_dir2')
    juicesync_cmd = ['./juicefs' , 'sync', '--dirs', jfs_source_dir,  'juicesync_dir1/']+sync_options
    print('juicesync_cmd: '+' '.join(juicesync_cmd))
    try:
        subprocess.check_call(juicesync_cmd)
    except Exception as e:
        assert False
    
    juicesync_cmd = ['./juicefs' , 'sync', '--dirs',  'juicesync_dir1/', 'juicesync_dir2/']+sync_options
    print('juicesync_cmd: '+' '.join(juicesync_cmd))
    try:
        subprocess.check_call(juicesync_cmd)
    except Exception as e:
        assert False

    diff_result = os.system('diff -ur juicesync_dir1 juicesync_dir2')
    assert diff_result==0

def compare_rsync_and_juicesync(sync_options):
    assert len(sync_options) != 0
    sync_options = [item for sublist in sync_options for item in sublist]
    if os.path.exists('rsync_dir/'):
        shutil.rmtree('rsync_dir/')
    os.makedirs('rsync_dir')
    rsync_cmd = ['rsync', '-a', '-r' , jfs_source_dir,  'rsync_dir/']+sync_options
    print('rsync_cmd: '+ ' '.join(rsync_cmd))
    try:
        subprocess.check_call(rsync_cmd)
    except Exception as e:
        assert False

    if os.path.exists('juicesync_dir/'):
        shutil.rmtree('juicesync_dir/')
    os.makedirs('juicesync_dir')
    juicesync_cmd = ['./juicefs' , 'sync', '--dirs', jfs_source_dir,  'juicesync_dir/']+sync_options
    print('juicesync_cmd: '+' '.join(juicesync_cmd))
    try:
        subprocess.check_call(juicesync_cmd)
    except Exception as e:
        assert False
    diff_result = os.system('diff -ur juicesync_dir rsync_dir')
    assert diff_result==0

if __name__ == "__main__":
    setup()
    test_sync_with_nested_dir()
    test_idempotent_for_juicesync()
    test_sync_with_path_entry()
    # test_sync_with_random_text()
