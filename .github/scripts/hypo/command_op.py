from ast import List
import hashlib
import json
import logging
import os
import pwd
import re
import shlex
import shutil
import stat
import subprocess
try: 
    __import__('xattr')
except ImportError:
    subprocess.check_call(["pip", "install", "xattr"])
import xattr
from common import is_jfs, get_acl, get_root, get_stat
from typing import Dict
try: 
    __import__('fallocate')
except ImportError:
    subprocess.check_call(["pip", "install", "fallocate"])
import fallocate
from context import Context
from stats import Statistics

class CommandOperation:
    JFS_CONTROL_FILES=['.accesslog', '.config', '.stats']
    stats = Statistics()
    def __init__(self, loggers: Dict[str, logging.Logger]):
        self.loggers = loggers

    def run_cmd(self, command:str, root_dir:str, stderr=subprocess.STDOUT) -> str:
        self.loggers[root_dir].info(f'run_cmd: {command}')
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
    
    #TODO: remove root_dir
    def handleException(self, e, root_dir, action, path, **kwargs):
        if isinstance(e, subprocess.CalledProcessError):
            err = e.output.decode()
        else:
            err = str(e)
        err = '\n'.join([elem.split('<FATAL>:')[-1].split('<ERROR>:')[-1] for elem in err.split('\n')])
        err = re.sub(r'\[\w+\.go:\d+\]', '', err)
        if err.find('setfacl') != -1 and err.find('\n') != -1:
            err = '\n'.join(sorted(err.split('\n')))
        self.stats.failure(action)
        self.loggers[root_dir].info(f'{action} {path} {kwargs} failed: {err}')
        return Exception(err)


    def do_mount(self, context:Context, mount, allow_other=True, enable_xattr=True, enable_acl=True, read_only=False, user='root'):
        command = f'sudo -u {user} {mount} mount {context.volume} {context.mp} --conf-dir={context.conf_dir} --no-update'
        if allow_other:
            command += ' -o allow_other'
        if enable_xattr:
            command += ' --enable-xattr'
        if enable_acl:
            command += ' --enable-acl'
        if read_only:
            command += ' --read-only'
        if context.cache_dir != '':
            command += f' --cache-dir={context.cache_dir}'
        try:
            output = self.run_cmd(command, context.root_dir)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, context.root_dir, 'do_mount', context.root_dir)
        return output
    
    def do_gateway(self, context:Context, mount, user='root'):
        command = f'sudo -u {user} MINIO_ROOT_USER=minioadmin MINIO_ROOT_PASSWORD=minioadmin {mount} gateway {context.volume} {context.gateway_address} --conf-dir={context.conf_dir} --no-update &'
        try:
            self.run_cmd(command, context.root_dir)
        except Exception as e:
            return self.handleException(e, context.root_dir, 'do_gateway', context.root_dir)
        return True
    
    def get_raw(self, size:str):
        # get bytes count from '4.00 KiB (4096 Bytes)' or '3 Bytes'
        if size.find('(') > -1:
            return size.split('(')[1].split(' ')[0]
        else:
            return size.split(' ')[0]

    def parse_info(self, info: str):
        li = info.split('\n')
        filename = li[0].split(':')[0].strip()
        # assert li[0].strip().startswith('inode:'), f'parse_info: {li[0]} should start with inode:'
        # inode = li[0].split(':')[1].strip()
        assert li[2].strip().startswith('files:'), f'parse_info: {li[1]} should start with files:'
        files = li[2].split(':')[1].strip()   
        assert li[3].strip().startswith('dirs:'), f'parse_info: {li[2]} should start with dirs:'  
        dirs = li[3].split(':')[1].strip()
        assert li[4].strip().startswith('length:'), f'parse_info: {li[3]} should start with length:'
        length = li[4].split(':')[1].strip()
        length = self.get_raw(length)
        assert li[5].strip().startswith('size:'), f'parse_info: {li[4]} should start with size:'
        size = li[5].split(':')[1].strip()
        size = self.get_raw(size)
        assert li[6].strip().startswith('path'), f'parse_info: {li[5]} should start with path:'
        paths = []
        if li[6].strip().startswith('path:'):
            paths.append(li[6].split(':')[1].strip())
        elif li[6].strip().startswith('paths:'):
            for i in range(7, len(li)):
                if li[i].strip().startswith('/'):
                    paths.append(li[i].strip())
                else:
                    break
        paths = ','.join(paths)
        return filename, files, dirs, length, size, paths

    def do_info(self, context:Context, entry, strict=True, user='root', raw=True, recuisive=False):
        abs_path = os.path.join(context.root_dir, entry)
        try:
            cmd = f'sudo -u {user} ./juicefs info {abs_path}'
            if raw:
                cmd += ' --raw'
            if recuisive:
                cmd += ' --recursive'
            if strict:
                cmd += ' --strict'
            result = self.run_cmd(cmd, context.root_dir)
            if '<ERROR>:' in result or "permission denied" in result:
                return self.handleException(Exception(result), context.root_dir, 'do_info', abs_path, **kwargs)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, context.root_dir, 'do_info', abs_path)
        result = self.parse_info(result)
        self.stats.success('do_info')
        self.loggers[context.root_dir].info(f'do_info {abs_path} succeed')
        return result 
    
    def do_rmr(self, context:Context, entry, user='root'):
        abspath = os.path.join(context.root_dir, entry)
        try:
            result = self.run_cmd(f'sudo -u {user} ./juicefs rmr {abspath}', context.root_dir)
            if '<ERROR>:' in result:
                return self.handleException(Exception(result), context.root_dir, 'do_rmr', abspath)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, context.root_dir, 'do_rmr', abspath)
        assert not os.path.exists(abspath), f'do_rmr: {abspath} should not exist'
        self.stats.success('do_rmr')
        self.loggers[context.root_dir].info(f'do_rmr {abspath} succeed')
        return True
    
    def do_status(self, context:Context):
        try:
            result = self.run_cmd(f'./juicefs status {context.meta_url}', context.root_dir, stderr=subprocess.DEVNULL)
            result = json.loads(result)['Setting']
        except subprocess.CalledProcessError as e:
            return self.handleException(e, context.root_dir, 'do_status', '')
        self.stats.success('do_status')
        self.loggers[context.root_dir].info(f'do_status succeed')
        return result['Storage'], result['Bucket'], result['BlockSize'], result['Compression'], \
            result['EncryptAlgo'], result['TrashDays'], result['MetaVersion'], \
            result['MinClientVersion'], result['DirStats'], result['EnableACL']
    
    def do_dump(self, context:Context, folder, fast=False, skip_trash=False, threads=1, keep_secret_key=False):
        abspath = os.path.join(context.root_dir, folder)
        subdir = os.path.relpath(abspath, context.mp)
        try:
            cmd=self.get_dump_cmd(context.meta_url, subdir, fast, skip_trash, keep_secret_key, threads)
            result = self.run_cmd(cmd, context.root_dir, stderr=subprocess.DEVNULL)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, context.root_dir, 'do_dump', abspath)
        self.stats.success('do_dump')
        self.loggers[context.root_dir].info(f'do_dump {abspath} succeed')
        return result

    def get_dump_cmd(self, meta_url, subdir, fast, skip_trash, keep_secret_key, threads):
        cmd = f'./juicefs dump {meta_url} '
        cmd += f' --subdir /{subdir}' if subdir != '' else ''
        cmd += f' --fast' if fast else ''
        cmd += f' --skip-trash' if skip_trash else ''
        cmd += f' --keep-secret-key' if keep_secret_key else ''
        cmd += f' --threads {threads}'
        return cmd

    def do_dump_load_dump(self, context:Context, folder, fast=False, skip_trash=False, threads=1, keep_secret_key=False):
        abspath = os.path.join(context.root_dir, folder)
        subdir = os.path.relpath(abspath, context.mp)
        try:
            cmd = self.get_dump_cmd(context.meta_url, subdir, fast, skip_trash, keep_secret_key, threads)
            result = self.run_cmd(cmd, context.root_dir, stderr=subprocess.DEVNULL)
            with open('dump.json', 'w') as f:
                f.write(result)
            if os.path.exists('load.db'):
                os.remove('load.db')
            self.run_cmd(f'./juicefs load sqlite3://load.db dump.json', context.root_dir)
            cmd = self.get_dump_cmd('sqlite3://load.db', '', fast, skip_trash, keep_secret_key, threads)
            result = self.run_cmd(cmd, context.root_dir, stderr=subprocess.DEVNULL)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, context.root_dir, 'do_dump', abspath)
        self.stats.success('do_dump')
        self.loggers[context.root_dir].info(f'do_dump {abspath} succeed')
        return result

    def do_warmup(self, context:Context, entry, user='root'):
        abspath = os.path.join(context.root_dir, entry)
        try:
            self.run_cmd(f'sudo -u {user} ./juicefs warmup {abspath}', context.root_dir)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, context.root_dir, 'do_warmup', abspath)
        self.stats.success('do_warmup')
        self.loggers[context.root_dir].info(f'do_warmup {abspath} succeed')
        return True

    def do_import(self, context:Context, mount, src_uri, dest_path, mode, user='root'):
        abspath = os.path.join(context.root_dir, dest_path)
        try:
            self.run_cmd(f'sudo -u {user} {mount} import {src_uri} {abspath} --mode {mode} --conf-dir={context.conf_dir}', context.root_dir)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, context.root_dir, 'do_import', abspath, src_uri=src_uri)
        self.stats.success('do_import')
        self.loggers[context.root_dir].info(f'do_import {src_uri} succeed')
        # src_uri is stared with /, so we need to remove the first /
        return self.do_info(context=context, mount=mount, entry=os.path.join(dest_path, src_uri[1:]))
    
    def do_gc(self, context:Context, compact:bool,  delete:bool, user:str='root'):
        try:
            cmd = f'sudo -u {user} ./juicefs gc {context.meta_url}'
            if compact:
                cmd += ' --compact'
            if delete:
                cmd += ' --delete'
            self.run_cmd(cmd, context.root_dir)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, context.root_dir, 'do_gc', '')
        self.stats.success('do_gc')
        self.loggers[context.root_dir].info(f'do_gc succeed')
        return True
    
    def do_clone(self, context:Context, entry, parent, new_entry_name, preserve:bool, user:str='root'):
        abspath = os.path.join(context.root_dir, entry)
        dest_abspath = os.path.join(context.root_dir, parent, new_entry_name)
        try:
            cmd = f'sudo -u {user} ./juicefs clone {abspath} {dest_abspath}'
            if preserve:
                cmd += ' --preserve'
            self.run_cmd(cmd, context.root_dir)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, context.root_dir, 'do_clone', '')
        self.stats.success('do_clone')
        self.loggers[context.root_dir].info(f'do_clone succeed')
        return True    
    
    def do_fsck(self, context:Context, entry, repair=False, recuisive=False, user='root'):
        abspath = os.path.join(context.root_dir, entry)
        try:
            cmd = f'sudo -u {user} ./juicefs fsck {context.meta_url} --path {abspath}'
            if repair:
                cmd += ' --repair'
            if recuisive:
                cmd += ' --recursive'
            self.run_cmd(cmd, context.root_dir, stderr=subprocess.DEVNULL)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, context.root_dir, 'do_fsck', '')
        self.stats.success('do_fsck')
        self.loggers[context.root_dir].info(f'do_fsck succeed')
        return True
    
    def do_quota_set(self, context:Context, mount, path, capacity, inodes, user='root'):
        abspath = os.path.join(context.root_dir, path)
        relative_path = os.path.relpath(abspath, os.path.join(context.mp))
        print(f'relative_path is {relative_path}')
        try:
            cmd = f'sudo -u {user} {mount} quota set {context.volume} --conf-dir {context.conf_dir} --path /{relative_path}'
            if capacity > -1 :
                cmd += f' --capacity {capacity}'
            if inodes > -1 :
                cmd += f' --inodes {inodes}'
            self.run_cmd(cmd, context.root_dir)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, context.root_dir, 'do_quota_set', abspath)
        self.stats.success('do_quota_set')
        self.loggers[context.root_dir].info(f'do_quota_set {abspath} succeed')
        return self.do_quota_get(context=context, mount=mount, path=path, user=user)
    
    def do_quota_delete(self, context:Context, mount, path, user='root'):
        abspath = os.path.join(context.root_dir, path)
        relative_path = os.path.relpath(abspath, os.path.join(context.mp))
        try:
            cmd = f'sudo -u {user} {mount} quota delete {context.volume} --conf-dir {context.conf_dir} --path /{relative_path}'
            self.run_cmd(cmd, context.root_dir)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, context.root_dir, 'do_quota_delete', abspath)
        self.stats.success('do_quota_delete')
        self.loggers[context.root_dir].info(f'do_quota_delete {abspath} succeed')
        return True
    
    def do_quota_get(self, context:Context, mount, path, user='root'):
        abspath = os.path.join(context.root_dir, path)
        relative_path = os.path.relpath(abspath, os.path.join(context.mp))
        try:
            cmd = f'sudo -u {user} {mount} quota get {context.volume} --conf-dir {context.conf_dir} --path /{relative_path}'
            result = self.run_cmd(cmd, context.root_dir)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, context.root_dir, 'do_quota_get', abspath)
        self.stats.success('do_quota_get')
        self.loggers[context.root_dir].info(f'do_quota_get {abspath} succeed')
        return result
    
    def do_quota_list(self, context:Context, mount, user='root'):
        try:
            cmd = f'sudo -u {user} {mount} quota list {context.volume} --conf-dir {context.conf_dir}'
            result = self.run_cmd(cmd, context.root_dir)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, context.root_dir, 'do_quota_list', '')
        self.stats.success('do_quota_list')
        self.loggers[context.root_dir].info(f'do_quota_list succeed')
        return result
    
    def do_trash_list(self, context:Context, user='root'):
        abspath = os.path.join(context.mp, '.trash')
        try:
            self.seteuid(user)
            li = os.listdir(abspath) 
            li = sorted(li)
        except Exception as e:
            return self.handleException(e, context.root_dir, 'do_trash_list', abspath, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
        self.stats.success('do_trash_list')
        self.loggers[context.root_dir].info(f'do_trash_list succeed')
        return tuple(li)
    
    def do_trash_restore(self, context:Context, index, user='root'):
        trash_list = self.do_trash_list(context=context)
        if len(trash_list) == 0:
            return ''
        index = index % len(trash_list)
        trash_file:str = trash_list[index]
        abspath = os.path.join(context.mp, '.trash', shlex.quote(trash_file))
        try:
            self.run_cmd(f'sudo -u {user} mv {abspath} {context.mp}', context.root_dir)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, context.root_dir, 'do_trash_restore', abspath, user=user)
        restored_path = os.path.join(context.mp, '/'.join(trash_file.split('|')[1:]))
        restored_path = os.path.relpath(restored_path, context.root_dir)
        self.stats.success('do_trash_restore')
        self.loggers[context.root_dir].info(f'do_trash_restore succeed')
        return restored_path
    