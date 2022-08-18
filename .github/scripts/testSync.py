import subprocess
import random
import shutil
from warnings import catch_warnings
from hypothesis import given, strategies as st, settings, example, assume
from hypothesis.strategies import composite, tuples
import os

entries = set()
jfs_source_dir=os.path.expanduser('~/Documents/juicefs2/pkg/')
jfs_source_dir='jfs_source/pkg/'


def setup():
    meta_url = "sqlite3://abc.db"
    # meta_url='redis://localhost/1'
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

    for root, dirs, files in os.walk(jfs_source_dir):
        # print(root)
        for d in dirs:
            print(d+'/')
            entries.add(d+'/')
        for file in files:
            print(file)
            entries.add(file)
            file_path = os.path.join(root, file)[len(jfs_source_dir):]
            print(file_path)
            entries.add(file_path)
    print(len(entries))

def change_entry(entries):
    li = []
    for entry in entries:
        type = random.choice(['--include', '--exclude'])
        # print(entry)
        value = entry.replace(random.choice(entry), random.choice(['*', '?']), 1)
        # print(value)
        li.append( (type, "'%s'"%value) )
    return li

setup()
path_entry = st.lists(st.sampled_from(list(entries))).map(lambda x: change_entry(x))
valid_name = st.text(st.characters(max_codepoint=1000, blacklist_categories=('Cc', 'Cs')), min_size=2).map(lambda s: s.strip()).filter(lambda s: len(s) > 0)
random_text = st.lists(valid_name).map(lambda x: change_entry(x))

@given(options=random_text)
@settings(max_examples=100, deadline=None)
# @example([['--exclude', "'1Ǹ[*ǣ'"]])
def test_sync_with_random_text(options):
    # pass
    # if sync_options == '--exclude *':
    #     # rsync does not support
    #     assume()
    sync(options)

@given(options=path_entry)
@settings(max_examples=100, deadline=None)
def test_sync_with_path_entry(options):
    sync(options)

def sync(sync_options):
    if not sync_options:
        return
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
    test_sync_with_path_entry()
    test_sync_with_random_text()
