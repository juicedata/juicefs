
import grp
import json
import logging
import os
import pwd
import subprocess
import sys
import stat
def red(s):
    return f'\033[31m{s}\033[0m'

def replace(src, old, new):
    if isinstance(src, str):
        return src.replace(old, new)
    elif isinstance(src, list) or isinstance(src, tuple):
        return [replace(x, old, new) for x in src]
    elif isinstance(src, dict):
        return {k: replace(v, old, new) for k, v in src.items()}
    else:
        return src
def run_cmd(command: str) -> str:
    print('run_cmd:'+command)
    if '|' in command or '>' in command:
        ret=os.system(command)
        if ret == 0:
            return ret
        else: 
            raise Exception(f"run command {command} failed with {ret}")
    try:
        output = subprocess.run(command.split(), check=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    except subprocess.CalledProcessError as e:
        # print(f'<FATAL>: subprocess run error: {e.output.decode()}')
        raise e
    # print(output.stdout.decode())
    # print('run_cmd succeed')
    return output.stdout.decode()


def setup_logger(log_file_path, logger_name, log_level='INFO'):
    if log_level == 'DEBUG':
        log_level = logging.DEBUG
    elif log_level == 'INFO':
        log_level = logging.INFO
    elif log_level == 'WARNING':
        log_level = logging.WARNING
    elif log_level == 'ERROR':
        log_level = logging.ERROR
    # Create a logger object
    assert os.path.exists(os.path.dirname(log_file_path)), red(f'setup_logger: {log_file_path} should exist')
    print(f'setup_logger {log_file_path}')
    logger = logging.getLogger(logger_name)
    logger.setLevel(logging.DEBUG)
    # Create a file handler for the logger
    file_handler = logging.FileHandler(log_file_path)
    file_handler.setLevel(logging.DEBUG)
    # Create a stream handler for the logger
    stream_handler = logging.StreamHandler()
    stream_handler.setLevel(log_level)
    # Create a formatter for the log messages
    formatter = logging.Formatter('%(asctime)s - %(levelname)s - %(message)s')
    file_handler.setFormatter(formatter)
    stream_handler.setFormatter(formatter)
    # Add the file and stream handlers to the logger
    logger.addHandler(file_handler)
    logger.addHandler(stream_handler)
    return logger

def is_jfs(path):
    root = get_root(path)
    file = os.path.join(root, '.jfsconfig')
    return os.path.isfile( file )

def get_root(path):
    path = os.path.abspath(path)
    d = path if os.path.isdir(path) else os.path.dirname(path)
    while d != '/':
        try:
            st = os.stat(d)
            if st.st_ino == 1:
                return d
        except:
            pass
        d = os.path.dirname(d)
    return d

def get_volume_name(path):
    root = get_root(path)
    file = os.path.join(root, '.config')
    if os.path.isfile(file):
        with open(file, 'r') as f:
            config = json.load(f)
            try :
                return config['Meta']['Volume']
            except KeyError:
                return config['Format']['Name']

def get_zones(dir):
    zones = []
    root = get_root(dir)
    for i in range(0, 8):
        try:
            zone = os.path.join(root, f'.jfszone{i}')
            os.stat(zone)
            zones.append(f'.jfszone{i}')
        except Exception as e:
            # print(f'zone {zone} not exist, {str(e)}')
            pass
    if len(zones) > 0:
        return zones
    else:
        return ['']   
    
def get_acl(abspath: str):
    s = run_cmd(f'getfacl {abspath}')
    lines = s.split('\n')
    # s = s.replace("# file: ", "# file: /")
    lines = [line for line in lines if not line.startswith("# file: ")]
    s = '\n'.join(lines)
    return s

def support_acl(path):
    root = get_root(path)
    file = os.path.join(root, '.config')
    if os.path.isfile(file):
        with open(file, 'r') as f:
            config = json.load(f)
            if config['Meta'].get('Args', '').find('--enable-acl') != -1:
                return True
            elif config['Format'].get('EnableACL', False):
                return True
            else:
                return False
    else:
        mount_point = subprocess.check_output(["df", root]).decode("utf-8").splitlines()[-1].split()[0]
        mount_options = subprocess.check_output(["sudo", "tune2fs", "-l", mount_point]).decode("utf-8")
        if "acl" not in mount_options:
            return False
        else:
            return True

def get_stat_field(st: os.stat_result):
    if stat.S_ISREG(st.st_mode):
        return st.st_gid, st.st_uid,  st.st_size, oct(st.st_mode), st.st_nlink
    elif stat.S_ISDIR(st.st_mode):
        return st.st_gid, st.st_uid, oct(st.st_mode)
    elif stat.S_ISLNK(st.st_mode):
        return st.st_gid, st.st_uid, oct(st.st_mode)
    else:
        return ()
    
    
def create_group(groupname):
    try:
        grp.getgrnam(groupname)
    except KeyError:
        subprocess.run(['groupadd', groupname], check=True)
        print(f"create Group {groupname}")

def create_user(user):
    try:
        pwd.getpwnam(user)
        subprocess.run(['usermod', '-g', user, '-G', '', user], check=True)
    except KeyError:
        subprocess.run(['useradd', '-g', user, '-G', '', user], check=True)
        print(f"create User {user} with group {user}")

def clean_dir(dir):
    try:
        subprocess.check_call(f'rm -rf {dir}'.split())
        assert not os.path.exists(dir), red(f'clean_dir: {dir} should not exist')
        subprocess.check_call(f'mkdir -p {dir}'.split())
        assert os.path.isdir(dir), red(f'clean_dir: {dir} should be dir')
    except subprocess.CalledProcessError as e:
        print(f'clean_dir {dir} failed:{e}, {e.returncode}, {e.output}')
        sys.exit(1)


def compare_content(dir1, dir2):
    os.system('find /tmp/fsrand  -type l ! -exec test -e {} \; -print > broken_symlink.log ')
    exclude_files = []
    with open('broken_symlink.log', 'r') as f:
        lines = f.readlines()
        for line in lines:
            filename = os.path.basename(line.strip())
            exclude_files.append(filename)
    exclude_options = [f'--exclude="{item}"' for item in exclude_files ]
    exclude_options = ' '.join(exclude_options)
    diff_command = f'diff -ur --no-dereference {dir1} {dir2} {exclude_options} 2>&1 |tee diff.log'
    print(diff_command)
    os.system(diff_command)
    with open('diff.log', 'r') as f:
        lines = f.readlines()
        filtered_lines = [line for line in lines if "recursive directory loop" not in line]
        assert len(filtered_lines) == 0, red(f'found diff: \n' + '\n'.join(filtered_lines))

def compare_stat(dir1, dir2):
    for root, dirs, files in os.walk(dir1):
        for file in files:
            path1 = os.path.join(root, file)
            path2 = os.path.join(dir2, os.path.relpath(path1, dir1))
            stat1 = get_stat_field(os.stat(path1))
            stat2 = get_stat_field(os.stat(path2))
            assert stat1 == stat2, red(f"{path1}: {stat1} and {path2}: {stat2} have different stats")
        for dir in dirs:
            path1 = os.path.join(root, dir)
            path2 = os.path.join(dir2, os.path.relpath(path1, dir1))
            stat1 = get_stat_field(os.stat(path1))
            stat2 = get_stat_field(os.stat(path2))
            assert stat1 == stat2, red(f"{path1}: {stat1} and {path2}: {stat2} have different stats")

def compare_acl(dir1, dir2):
    for root, dirs, files in os.walk(dir1):
        for file in files:
            path1 = os.path.join(root, file)
            path2 = os.path.join(dir2, os.path.relpath(path1, dir1))
            if os.path.exists(path2):
                acl1 = get_acl(path1)
                acl2 = get_acl(path2)
                assert acl1 == acl2, red(f"{path1}: {acl1} and {path2}: {acl2} have different acl")
        for dir in dirs:
            path1 = os.path.join(root, dir)
            path2 = os.path.join(dir2, os.path.relpath(path1, dir1))
            if os.path.exists(path2):
                acl1 = get_acl(path1)
                acl2 = get_acl(path2)
                assert acl1 == acl2, red(f"{path1}: {acl1} and {path2}: {acl2} have different acl")
