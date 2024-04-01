import hashlib
import os
import subprocess
try: 
    __import__('xattr')
except ImportError:
    subprocess.check_call(["pip", "install", "xattr"])
try: 
    __import__('fallocate')
except ImportError:
    subprocess.check_call(["pip", "install", "fallocate"])
from stats import Statistics
from minio.error import S3Error
import common
from minio import Minio
import io


class S3Client(Minio):
    stats = Statistics()
    def __init__(self, alias, url, access_key, secret_key):
        super().__init__(
            url,
            access_key=access_key,
            secret_key=secret_key,
            secure=False
        )
        self.alias = alias
        self.url = url
        self.access_key = access_key
        self.secret_key = secret_key
        log_level=os.environ.get('LOG_LEVEL', 'INFO')
        self.logger = common.setup_logger(f'./{alias}.log', f'{alias}', log_level)
        self.run_cmd(f'mc alias set {alias} http://{url} {access_key} {secret_key}')

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
    
    def handleException(self, e, action, **kwargs):
        self.stats.failure(action)
        self.logger.info(f'{action} {kwargs} failed: {e}')
        if isinstance(e, S3Error):
            return Exception(f'code:{e.code} message:{e.message}')
        elif isinstance(e, subprocess.CalledProcessError):
            return Exception(f'returncode:{e.returncode} output:{e.output.decode()}')
        else:
            return e
    
    def remove_all_buckets(self):
        buckets = self.list_buckets()
        for bucket in buckets:
            bucket_name = bucket.name
            objects = self.list_objects(bucket_name, recursive=True)
            for obj in objects:
                self.remove_object(bucket_name, obj.object_name)
            self.remove_bucket(bucket_name)
            print(f"Bucket '{bucket_name}' removed successfully.")
        
    def do_list_buckets(self):
        try:
            buckets = self.list_buckets()
        except S3Error as e:
            return self.handleException(e, 'do_list_buckets')
        self.stats.success('do_list_buckets')
        self.logger.info(f'do_list_buckets succeed')
        return sorted([bucket.name for bucket in buckets])
    
    def do_remove_bucket(self, bucket_name:str):
        try:
            self.remove_bucket(bucket_name)
        except S3Error as e:
            return self.handleException(e, 'do_remove_bucket', bucket_name=bucket_name)
        assert not self.bucket_exists(bucket_name)
        self.stats.success('do_remove_bucket')
        self.logger.info(f'do_remove_bucket {bucket_name} succeed')
        return True

    def do_create_bucket(self, bucket_name:str):
        try:
            self.make_bucket(bucket_name)
            print(f"Bucket '{bucket_name}' created successfully.")
        except S3Error as e:
            return self.handleException(e, 'do_create_bucket', bucket_name=bucket_name)
        assert self.bucket_exists(bucket_name)
        self.stats.success('do_create_bucket')
        self.logger.info(f'do_create_bucket {bucket_name}  succeed')
        return True
    
    def do_set_bucket_policy(self, bucket_name:str, policy:str):
        try:
            self.set_bucket_policy(bucket_name, policy)
        except S3Error as e:
            return self.handleException(e, 'do_set_bucket_policy', bucket_name=bucket_name, policy=policy)
        self.stats.success('do_set_bucket_policy')
        self.logger.info(f'do_set_bucket_policy {bucket_name} succeed')
        return True
    
    def do_get_bucket_policy(self, bucket_name:str):
        try:
            policy = self.get_bucket_policy(bucket_name)
        except S3Error as e:
            return self.handleException(e, 'do_get_bucket_policy', bucket_name=bucket_name)
        self.stats.success('do_get_bucket_policy')
        self.logger.info(f'do_get_bucket_policy {bucket_name} succeed')
        return policy

    def do_delete_bucket_policy(self, bucket_name:str):
        try:
            self.delete_bucket_policy(bucket_name)
        except S3Error as e:
            return self.handleException(e, 'do_delete_bucket_policy', bucket_name=bucket_name)
        self.stats.success('do_delete_bucket_policy')
        self.logger.info(f'do_delete_bucket_policy {bucket_name} succeed')
        return True

    def do_stat_object(self, bucket_name:str, object_name:str):
        try:
            stat = self.stat_object(bucket_name, object_name)
        except S3Error as e:
            return self.handleException(e, 'do_stat_object', bucket_name=bucket_name, object_name=object_name)
        finally:
            pass
        self.stats.success('do_stat_object')
        self.logger.info(f'do_stat_object {bucket_name} {object_name} succeed')
        sorted_stat = sorted(stat.__dict__.items())
        stat_str = "\n".join([f"{key}: {value}" for key, value in sorted_stat])
        # print(stat_str)
        return stat.bucket_name, stat.object_name, stat.size

    def do_put_object(self, bucket_name:str, object_name:str, data, length, content_type='application/octet-stream', part_size=5*1024*1024):
        try:
            self.put_object(bucket_name, object_name, io.BytesIO(data), length=length, content_type=content_type, part_size=part_size)
        except S3Error as e:
            return self.handleException(e, 'do_put_object', bucket_name=bucket_name, object_name=object_name, length=length, part_size=part_size)
        self.stats.success('do_put_object')
        self.logger.info(f'do_put_object {bucket_name} {object_name} succeed')
        return self.do_stat_object(bucket_name, object_name)

    def do_get_object(self, bucket_name:str, object_name:str, offset=0, length=0):
        try:
            stat = self.stat_object(bucket_name, object_name)
            if stat.size == 0:
                offset = 0
            else:
                offset = offset % stat.size
            if length > stat.size - offset:
                length = stat.size - offset
            response = self.get_object(bucket_name, object_name, offset=offset, length=length)
            md5_hash = hashlib.md5()
            for data in response.stream(32*1024):
                md5_hash.update(data)
            md5_hex = md5_hash.hexdigest()
        except S3Error as e:
            return self.handleException(e, 'do_get_object', bucket_name=bucket_name, object_name=object_name, offset=offset, length=length)
        self.stats.success('do_get_object')
        self.logger.info(f'do_get_object {bucket_name} {object_name} succeed')
        return md5_hex

    def do_fput_object(self, bucket_name:str, object_name:str, src_path:str):
        try:
            self.fput_object(bucket_name, object_name, src_path)
        except S3Error as e:
            return self.handleException(e, 'do_fput_object', bucket_name=bucket_name, obj_name=object_name, src_path=src_path)
        self.stats.success('do_fput_object')
        self.logger.info(f'do_fput_object {bucket_name} {object_name} {src_path} succeed')
        return self.do_stat_object(bucket_name, object_name)
    
    def do_fget_object(self, bucket_name:str, object_name:str, file_path:str):
        try:
            self.fget_object(bucket_name, object_name, file_path)
        except S3Error as e:
            return self.handleException(e, 'do_fget_object', bucket_name=bucket_name, object_name=object_name, file_path=file_path)
        assert(os.path.exists(file_path))
        self.stats.success('do_fget_object')
        self.logger.info(f'do_fget_object {bucket_name} {object_name} {file_path} succeed')
        return os.stat(file_path).st_size

    def object_exists(self, bucket_name:str, object_name:str):
        try:
            self.stat_object(bucket_name, object_name)
            return True
        except S3Error as e:
            if e.code == "NoSuchKey":
                return False
            else:
                raise e

    def do_remove_object(self, bucket_name:str, object_name:str):
        try:
            self.remove_object(bucket_name, object_name)
        except S3Error as e:
            return self.handleException(e, 'do_remove_object', bucket_name=bucket_name, object_name=object_name)
        assert not self.object_exists(bucket_name, object_name)
        self.stats.success('do_remove_object')
        self.logger.info(f'do_remove_object {bucket_name} {object_name} succeed')
        return True
    
    def do_list_objects(self, bucket_name, prefix, start_after, include_user_meta, include_version, use_url_encoding_type, recursive):
        try:
            objects = self.list_objects(bucket_name, prefix=prefix, start_after=start_after, include_user_meta=include_user_meta, include_version=include_version, use_url_encoding_type=use_url_encoding_type, recursive=recursive)
        except S3Error as e:
            return self.handleException(e, 'do_list_objects', bucket_name=bucket_name, prefix=prefix, start_after=start_after, include_user_meta=include_user_meta, include_version=include_version, use_url_encoding_type=use_url_encoding_type, recursive=recursive)
        self.stats.success('do_list_objects')
        self.logger.info(f'do_list_objects {bucket_name} {prefix} {start_after} {include_user_meta} {include_version} {use_url_encoding_type} {recursive} succeed')
        result = '\n'.join([f'{obj.object_name} {obj.size} {obj.etag}' for obj in objects])
        return result
    
    def do_add_user(self, access_key, secret_key):
        try:
            self.run_cmd(f'mc admin user add {self.alias} {access_key} {secret_key}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_add_user', access_key=access_key, secret_key=secret_key)
        self.stats.success('do_add_user')
        self.logger.info(f'do_add_user {access_key} succeed')
        return True
    
    def do_remove_user(self, access_key):
        try:
            self.run_cmd(f'mc admin user remove {self.alias} {access_key}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_remove_user', access_key=access_key)
        self.stats.success('do_remove_user')
        self.logger.info(f'do_remove_user {access_key} succeed')
        return True

    def do_add_group(self, group_name, members):
        try:
            self.run_cmd(f'mc admin group add {self.alias} {group_name} {" ".join(members)}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_add_group', group_name=group_name)
        self.stats.success('do_add_group')
        self.logger.info(f'do_add_group {group_name} succeed')
        return self.do_group_info(group_name)

    def do_remove_group(self, group_name):
        try:
            self.run_cmd(f'mc admin group remove {self.alias} {group_name}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_remove_group', group_name=group_name)
        self.stats.success('do_remove_group')
        self.logger.info(f'do_remove_group {group_name} succeed')
        return True

    def do_group_info(self, group_name):
        try:
            self.run_cmd(f'mc admin group info {self.alias} {group_name}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_group_info', group_name=group_name)
        self.stats.success('do_group_info')
        self.logger.info(f'do_group_info {group_name} succeed')
        return True