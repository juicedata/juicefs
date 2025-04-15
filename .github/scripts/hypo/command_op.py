import json
import os
import pwd
import re
import shlex
import subprocess
try: 
    __import__('xattr')
except ImportError:
    subprocess.check_call(["pip", "install", "xattr"])
try: 
    __import__('psutil')
except ImportError:
    subprocess.check_call(["pip", "install", "psutil"])
import psutil
from stats import Statistics
import common


class CommandOperation:
    JFS_CONTROL_FILES=['.accesslog', '.config', '.stats']
    stats = Statistics()
    def __init__(self, name, mp, root_dir):
        self.logger = common.setup_logger(f'./{name}.log', name, os.environ.get('LOG_LEVEL', 'INFO'))
        self.name = name
        self.mp = mp
        self.root_dir = root_dir
        self.meta_url = self.get_meta_url(mp)
                
    def guess_password(self, meta_url):
        if '****' not in meta_url:
            return meta_url
        if meta_url.startswith('postgres://'):
            return meta_url.replace('****', 'postgres')
        else:
            return meta_url.replace('****', 'root')

    def get_meta_url(self, mp):
        with open(os.path.join(mp, '.config')) as f:
            config = json.loads(f.read())
            pid = config['Pid']
            process = psutil.Process(pid)
            cmdline = process.cmdline()
            for item in cmdline:
                if ' ' in item:
                    for subitem in item.split(' '):
                        if '://' in subitem:
                            return self.guess_password(subitem)
                elif '://' in item:
                    return self.guess_password(item)
            raise Exception(f'get_meta_url: {cmdline} does not contain meta url')
        
    def run_cmd(self, command:str, stderr=subprocess.STDOUT) -> str:
        self.logger.info(f'run_cmd: {command}')
        if '|' in command or '>' in command or '&' in command:
            ret=os.system(command)
            if ret == 0:
                return ret
            else: 
                raise Exception(f"run command {command} failed with {ret}")
        try:
            output = subprocess.run(command.split(), check=True, stdout=subprocess.PIPE, stderr=stderr)
        except subprocess.CalledProcessError as e:
            raise e
        return output.stdout.decode()

    def seteuid(self, user):
        os.seteuid(pwd.getpwnam(user).pw_uid)
        os.setegid(pwd.getpwnam(user).pw_gid)
    
    def handleException(self, e, action, path, **kwargs):
        if isinstance(e, subprocess.CalledProcessError):
            err = e.output.decode()
        else:
            err = str(e)
        err = '\n'.join([elem.split('<FATAL>:')[-1].split('<ERROR>:')[-1] for elem in err.split('\n')])
        err = re.sub(r'\[\w+\.go:\d+\]', '', err)
        if err.find('setfacl') != -1 and err.find('\n') != -1:
            err = '\n'.join(sorted(err.split('\n')))
        self.stats.failure(action)
        self.logger.info(f'{action} {path} {kwargs} failed: {err}')
        return Exception(err)

    def get_raw(self, size:str):
        # get bytes count from '4.00 KiB (4096 Bytes)' or '3 Bytes'
        if size.find('(') > -1:
            return size.split('(')[1].split(' ')[0]
        else:
            return size.split(' ')[0]

    def parse_info(self, info: str):
        li = info.split('\n')
        if "GOCOVERDIR" in li[0]:
            li = li[1:]
        filename = li[0].split(':')[0].strip()
        # assert li[0].strip().startswith('inode:'), f'parse_info: {li[0]} should start with inode:'
        # inode = li[0].split(':')[1].strip()
        assert li[2].strip().startswith('files:'), f'parse_info: {li[2]} should start with files:'
        files = li[2].split(':')[1].strip()   
        assert li[3].strip().startswith('dirs:'), f'parse_info: {li[3]} should start with dirs:'  
        dirs = li[3].split(':')[1].strip()
        assert li[4].strip().startswith('length:'), f'parse_info: {li[4]} should start with length:'
        length = li[4].split(':')[1].strip()
        length = self.get_raw(length)
        assert li[5].strip().startswith('size:'), f'parse_info: {li[5]} should start with size:'
        size = li[5].split(':')[1].strip()
        size = self.get_raw(size)
        assert li[6].strip().startswith('path'), f'parse_info: {li[6]} should start with path:'
        paths = []
        if li[6].strip().startswith('path:'):
            paths.append(li[6].split(':')[1].strip())
        elif li[6].strip().startswith('paths:'):
            for i in range(7, len(li)):
                if li[i].strip().startswith('/'):
                    paths.append(li[i].strip())
                else:
                    break
        paths = ','.join(sorted(paths))
        return filename, files, dirs, length, size, paths

    def do_info(self, entry, strict=True, user='root', raw=True, recuisive=False):
        abs_path = os.path.join(self.root_dir, entry)
        try:
            cmd = f'sudo -u {user} ./juicefs info --log-level error {abs_path}'
            if raw:
                cmd += ' --raw'
            if recuisive:
                cmd += ' --recursive'
            if strict:
                cmd += ' --strict'
            result = self.run_cmd(cmd)
            if '<ERROR>:' in result or "permission denied" in result:
                return self.handleException(Exception(result), 'do_info', abs_path)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_info', abs_path)
        result = self.parse_info(result)
        self.stats.success('do_info')
        self.logger.info(f'do_info {abs_path} succeed')
        return result 
    
    def do_rmr(self, entry, user='root'):
        abspath = os.path.join(self.root_dir, entry)
        try:
            result = self.run_cmd(f'sudo -u {user} ./juicefs rmr --log-level error {abspath}')
            if '<ERROR>:' in result:
                return self.handleException(Exception(result), 'do_rmr', abspath)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_rmr', abspath)
        assert not os.path.exists(abspath), f'do_rmr: {abspath} should not exist'
        self.stats.success('do_rmr')
        self.logger.info(f'do_rmr {abspath} succeed')
        return True
    
    def do_status(self):
        try:
            result = self.run_cmd(f'./juicefs status {self.meta_url} --log-level error', stderr=subprocess.DEVNULL)
            result = json.loads(result)['Setting']
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_status', '')
        self.stats.success('do_status')
        self.logger.info(f'do_status succeed')
        return result['Storage'], result['Bucket'], result['BlockSize'], result['Compression'], \
            result['EncryptAlgo'], result['TrashDays'], result['MetaVersion'], \
            result['MinClientVersion'], result['DirStats'], result['EnableACL']
    
    def do_dump(self, folder, fast=False, skip_trash=False, threads=1, keep_secret_key=False, user='root'):
        abspath = os.path.join(self.root_dir, folder)
        subdir = os.path.relpath(abspath, self.mp)
        try:
            # compact before dump to avoid slice difference
            self.do_compact(folder)
            cmd=self.get_dump_cmd(self.meta_url, subdir, fast, skip_trash, keep_secret_key, threads, user)
            result = self.run_cmd(cmd, stderr=subprocess.DEVNULL)
        except subprocess.CalledProcessError as e:
            return self.handleException(e,  'do_dump', abspath)
        self.stats.success('do_dump')
        self.logger.info(f'do_dump {abspath} succeed')
        # with open(f'dump_{self.name}.json', 'w') as f:
        #     f.write(self.clean_dump(result))
        return self.clean_dump(result)

    def get_dump_cmd(self, meta_url, subdir, fast, skip_trash, keep_secret_key, threads, user='root'):
        cmd = f'sudo -u {user} ./juicefs dump --log-level error {meta_url} '
        cmd += f' --subdir /{subdir}' if subdir != '' else ''
        cmd += f' --fast' if fast else ''
        cmd += f' --skip-trash' if skip_trash else ''
        cmd += f' --keep-secret-key' if keep_secret_key else ''
        cmd += f' --threads {threads}'
        cmd += f' --log-level error'
        return cmd

    def do_dump_load_dump(self, folder, fast=False, skip_trash=False, threads=1, keep_secret_key=False, user='root'):
        abspath = os.path.join(self.root_dir, folder)
        subdir = os.path.relpath(abspath, self.mp)
        try:
            print(f'meta_url is {self.meta_url}')
            cmd = self.get_dump_cmd(self.meta_url, subdir, fast, skip_trash, keep_secret_key, threads, user)
            result = self.run_cmd(cmd, stderr=subprocess.DEVNULL)
            with open('dump.json', 'w') as f:
                f.write(result)
            if os.path.exists('load.db'):
                os.remove('load.db')
            self.run_cmd(f'sudo -u {user} ./juicefs load sqlite3://load.db dump.json')
            cmd = self.get_dump_cmd('sqlite3://load.db', '', fast, skip_trash, keep_secret_key, threads, user)
            result = self.run_cmd(cmd, stderr=subprocess.DEVNULL)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_dump', abspath)
        self.stats.success('do_dump')
        self.logger.info(f'do_dump {abspath} succeed')
        return self.clean_dump(result)

    def clean_dump(self, dump):
        lines = dump.split('\n')
        new_lines = []
        exclude_keys = ['Name', 'UUID', 'usedSpace', 'usedInodes', 'nextInodes', 'nextChunk', 'nextTrash', 'nextSession']
        reset_keys = ['id', 'inode', 'atimensec', 'mtimensec', 'ctimensec', 'atime', 'ctime', 'mtime']
        for line in lines:
            should_delete = False
            for key in exclude_keys:
                if f'"{key}"' in line:
                    should_delete = True
                    break
            if should_delete:
                continue
            for key in reset_keys:
                if f'"{key}"' in line:
                    pattern = rf'"{key}":(\d+)'
                    line = re.sub(pattern, f'"{key}":0', line)
            new_lines.append(line)
        return '\n'.join(new_lines)

    def do_warmup(self, entry, user='root'):
        abspath = os.path.join(self.root_dir, entry)
        try:
            self.run_cmd(f'sudo -u {user} ./juicefs warmup --log-level error {abspath}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_warmup', abspath)
        self.stats.success('do_warmup')
        self.logger.info(f'do_warmup {abspath} succeed')
        return True

    def do_gc(self, compact:bool,  delete:bool, user:str='root'):
        try:
            cmd = f'sudo -u {user} ./juicefs gc --log-level error {self.meta_url}'
            if compact:
                cmd += ' --compact'
            if delete:
                cmd += ' --delete'
            self.run_cmd(cmd)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_gc', '')
        self.stats.success('do_gc')
        self.logger.info(f'do_gc succeed')
        return True
    
    def do_clone(self, entry, parent, new_entry_name, preserve:bool, user:str='root'):
        abspath = os.path.join(self.root_dir, entry)
        dest_abspath = os.path.join(self.root_dir, parent, new_entry_name)
        try:
            cmd = f'sudo -u {user} ./juicefs clone --log-level error {abspath} {dest_abspath}'
            if preserve:
                cmd += ' --preserve'
            self.run_cmd(cmd)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_clone', '')
        self.stats.success('do_clone')
        self.logger.info(f'do_clone succeed')
        return True    
    
    def do_fsck(self, entry, repair=False, recuisive=False, user='root'):
        abspath = os.path.join(self.root_dir, entry)
        try:
            cmd = f'sudo -u {user} ./juicefs fsck --log-level error {self.meta_url} --path {abspath}'
            if repair:
                cmd += ' --repair'
            if recuisive:
                cmd += ' --recursive'
            self.run_cmd(cmd, stderr=subprocess.DEVNULL)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_fsck', '')
        self.stats.success('do_fsck')
        self.logger.info(f'do_fsck succeed')
        return True
    
    def do_trash_list(self, user='root'):
        abspath = os.path.join(self.mp, '.trash')
        try:
            self.seteuid(user)
            li = os.listdir(abspath) 
            li = sorted(li)
        except Exception as e:
            return self.handleException(e, 'do_trash_list', abspath, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
        self.stats.success('do_trash_list')
        self.logger.info(f'do_trash_list succeed')
        return tuple(li)
    
    def do_restore(self, put_back, threads, user='root'):
        abspath = os.path.join(self.mp, '.trash')
        try:
            li = os.listdir(abspath)
            for trash_dir in li:
                cmd = f'sudo -u {user} ./juicefs restore {trash_dir} --threads {threads}'
                if put_back:
                    cmd += ' --put-back'
                self.run_cmd(cmd)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_restore', abspath, user=user)
        self.stats.success('do_restore')
        self.logger.info(f'do_restore succeed')
        return True

    def do_trash_restore(self, index, user='root'):
        trash_list = self.do_trash_list()
        if len(trash_list) == 0:
            return ''
        index = index % len(trash_list)
        trash_file:str = trash_list[index]
        abspath = os.path.join(self.mp, '.trash', shlex.quote(trash_file))
        try:
            self.run_cmd(f'sudo -u {user} mv {abspath} {self.mp}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_trash_restore', abspath, user=user)
        restored_path = os.path.join(self.mp, '/'.join(trash_file.split('|')[1:]))
        restored_path = os.path.relpath(restored_path, self.root_dir)
        self.stats.success('do_trash_restore')
        self.logger.info(f'do_trash_restore succeed')
        return restored_path
    
    def do_compact(self, entry, threads=5, user='root'):
        path = os.path.join(self.root_dir, entry)
        try:
            self.run_cmd(f'sudo -u {user} ./juicefs compact --log-level error {path} --threads {threads}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_compact', path, user=user)
        self.stats.success('do_compact')
        self.logger.info(f'do_compact succeed')
        return True
    
    def do_config(self, capacity, inodes, trash_days, enable_acl, encrypt_secret, force, yes, user):
        try:
            cmd = f'sudo -u {user} ./juicefs config {self.meta_url} --capacity {capacity} --inodes {inodes} --trash-days {trash_days} --enable-acl {enable_acl} --encrypt-secret {encrypt_secret}'
            if force:
                cmd += ' --force'
            if yes:
                cmd += ' --yes'
            self.run_cmd(cmd)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_config', '')
        self.stats.success('do_config')
        self.logger.info(f'do_config succeed')
        return True