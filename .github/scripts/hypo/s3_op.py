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
from stats import Statistics
from minio.error import S3Error
import common
from minio import Minio


class S3Client(Minio):
    stats = Statistics()
    def __init__(self, name, url, access_key, secret_key):
        super().__init__(
            url,
            access_key=access_key,
            secret_key=secret_key,
            secure=False
        )
        self.name = name
        self.url = url
        self.access_key = access_key
        self.secret_key = secret_key
        log_level=os.environ.get('LOG_LEVEL', 'INFO')
        self.logger = common.setup_logger(f'./{name}.log', f'{name}', log_level)

    def run_cmd(self, command:str, root_dir:str, stderr=subprocess.STDOUT) -> str:
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
    
    def handleException(self, e, action, **kwargs):
        if isinstance(e, subprocess.CalledProcessError):
            err = e.output.decode()
        else:
            err = str(e)
        self.stats.failure(action)
        self.logger.info(f'{action} {kwargs} failed: {err}')
        return Exception(err)
    
    def do_create_bucket(self, bucket_name:str):
        try:
            if self.bucket_exists(bucket_name):
                objects = self.list_objects(bucket_name, recursive=True)
                for obj in objects:
                    self.remove_object(bucket_name, obj.object_name)
                self.remove_bucket(bucket_name)
                print(f"Bucket '{bucket_name}' removed successfully.")
            self.make_bucket(bucket_name)
            print(f"Bucket '{bucket_name}' created successfully.")
        except S3Error as e:
            raise e
    
    def do_stat_object(self, bucket:str, obj_name:str):
        try:
            stat = self.stat_object(bucket, obj_name)
        except Exception as e:
            return self.handleException(e, 'do_stat_object', bucket=bucket, obj_name=obj_name)
        finally:
            pass
        self.stats.success('do_stat_object')
        self.logger.info(f'do_stat_object {bucket} {obj_name} succeed')
        sorted_stat = sorted(stat.__dict__.items())
        stat_str = "\n".join([f"{key}: {value}" for key, value in sorted_stat])
        print(stat_str)
        return stat_str

    def do_put_object(self, bucket:str, obj_name:str, src_path:str):
        try:
            self.fput_object(
                bucket, obj_name, src_path)
        except Exception as e:
            return self.handleException(e, 'do_put_object', bucket=bucket, obj_name=obj_name, src_path=src_path)
        finally:
            pass
        self.stats.success('do_put_object')
        self.logger.info(f'do_put_object {bucket} {obj_name} {src_path} succeed')
        return self.do_stat_object(bucket, obj_name)
    

    