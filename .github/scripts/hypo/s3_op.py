import hashlib
import json
import os
import re
import subprocess
try: 
    __import__('xattr')
except ImportError:
    subprocess.check_call(["pip", "install", "xattr"])
try: 
    __import__('minio')
except ImportError:
    subprocess.check_call(["pip", "install", "minio"])
try: 
    __import__('fallocate')
except ImportError:
    subprocess.check_call(["pip", "install", "fallocate"])
from stats import Statistics
from minio.error import S3Error
import common
from minio import Minio
import io
from s3_contant import *

class S3Client():
    stats = Statistics()
    def __init__(self, prefix, url, url2=None):
        self.prefix = prefix
        self.url = url
        self.url2 = url2
        log_level=os.environ.get('LOG_LEVEL', 'INFO')
        self.logger = common.setup_logger(f'./{prefix}.log', f'{prefix}', log_level)

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
    
    def sort_dict(self, obj):
        if isinstance(obj, dict):
            return {k: self.sort_dict(v) for k, v in obj.items()}
        elif isinstance(obj, list) and all(isinstance(elem, (int, float, str)) for elem in obj):
            return sorted(obj)
        elif isinstance(obj, list) and all(isinstance(elem, dict) for elem in obj):
            return [self.sort_dict(elem) for elem in obj]
        else:
            return obj
        
    def handleException(self, e, action, **kwargs):
        self.stats.failure(action)
        if isinstance(e, S3Error):
            self.logger.info(f'{action} {kwargs} failed: {e}')
            return Exception(f'code:{e.code} message:{e.message}')
        elif isinstance(e, subprocess.CalledProcessError):
            self.logger.info(f'{action} {kwargs} failed: {e.output.decode()}')
            try:
                output = json.loads(e.output.decode())
                message = output.get('error', {}).get('message', 'error message not found')
                return Exception(f'returncode:{e.returncode} {message}')
            except ValueError as ve:
                output = e.output.decode()
                output = re.sub(r'\b\d+\.\d+\b|\b\d+\b', '***', output)
                return Exception(f'returncode:{e.returncode} output:{output}')
        else:
            self.logger.info(f'{action} {kwargs} failed: {e}')
            return e
        
    def do_info(self, alias):
        try:
            self.run_cmd(f'mc admin info {self.get_alias(alias)}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_info')
        self.stats.success('do_info')
        self.logger.info(f'do_info succeed')
        return True

    def remove_all_buckets(self):
        client=Minio(self.url,access_key=ROOT_ACCESS_KEY,secret_key=ROOT_SECRET_KEY,secure=False)
        buckets = client.list_buckets()
        for bucket in buckets:
            bucket_name = bucket.name
            objects = client.list_objects(bucket_name, recursive=True)
            for obj in objects:
                client.remove_object(bucket_name, obj.object_name)
            client.remove_bucket(bucket_name)
            print(f"Bucket '{bucket_name}' removed successfully.")
        
    def do_list_buckets(self, alias):
        try:
            result = self.run_cmd(f'mc ls {self.get_alias(alias)}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_list_buckets')
        self.stats.success('do_list_buckets')
        self.logger.info(f'do_list_buckets succeed')
        result = [item.split()[-1][:-1] for item in result.split("\n") if item.strip()]
        # print(result)
        return sorted(result)
    
    def do_remove_bucket(self, bucket_name:str, alias):
        try:
            self.run_cmd(f'mc rb {self.get_alias(alias)}/{bucket_name}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_remove_bucket', bucket_name=bucket_name, alias=alias)
        self.stats.success('do_remove_bucket')
        self.logger.info(f'do_remove_bucket {alias} {bucket_name} succeed')
        return True

    def do_create_bucket(self, bucket_name:str, alias):
        try:
            self.run_cmd(f'mc mb {self.get_alias(alias)}/{bucket_name}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_create_bucket', bucket_name=bucket_name)
        self.stats.success('do_create_bucket')
        self.logger.info(f'do_create_bucket {bucket_name} succeed')
        return True

    def do_set_bucket_policy(self, bucket_name:str, policy:str, alias):
        try:
            self.run_cmd(f'mc policy set {policy} {self.get_alias(alias)}/{bucket_name}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_set_bucket_policy', bucket_name=bucket_name, policy=policy)
        self.stats.success('do_set_bucket_policy')
        self.logger.info(f'do_set_bucket_policy {bucket_name} {policy} succeed')
        return True
    
    def do_get_bucket_policy(self, bucket_name:str, alias):
        try:
            result = self.run_cmd(f'mc policy get {self.get_alias(alias)}/{bucket_name} --json')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_get_bucket_policy', bucket_name=bucket_name)
        self.stats.success('do_get_bucket_policy')
        self.logger.info(f'do_get_bucket_policy {bucket_name} succeed')
        return self.sort_dict(json.loads(result))

    def do_list_bucket_policy(self, bucket_name:str, alias, recursive=False):
        try:
            cmd = f'mc policy list {self.get_alias(alias)}/{bucket_name}'
            if recursive:
                cmd += ' --recursive'
            result = self.run_cmd(cmd)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_list_bucket_policy', bucket_name=bucket_name)
        self.stats.success('do_list_bucket_policy')
        self.logger.info(f'do_list_bucket_policy {bucket_name} succeed')
        return sorted(result.split("\n"))

    def do_stat_object(self, bucket_name:str, object_name:str, alias):
        try:
            result = self.run_cmd(f'mc stat {self.get_alias(alias)}/{bucket_name}/{object_name} ')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_stat_object', bucket_name=bucket_name, object_name=object_name)
        stat = {}
        for line in result.split('\n'):
            if line.strip() and ':' in line:
                key, value = line.split(':', 1)
                stat[key.strip()] = value.strip()
        self.stats.success('do_stat_object')
        self.logger.info(f'do_stat_object {bucket_name} {object_name} succeed')
        # print(stat)
        return stat['Name'], stat['Size'], stat['ETag'], stat['Type']

    def do_put_object(self, bucket_name:str, object_name:str, data, length, content_type='application/octet-stream', part_size=5*1024*1024):
        client=Minio(self.url,access_key=ROOT_ACCESS_KEY,secret_key=ROOT_SECRET_KEY,secure=False)
        try:
            client.put_object(bucket_name, object_name, io.BytesIO(data), length=length, content_type=content_type, part_size=part_size)
        except S3Error as e:
            return self.handleException(e, 'do_put_object', bucket_name=bucket_name, object_name=object_name, length=length, part_size=part_size)
        self.stats.success('do_put_object')
        self.logger.info(f'do_put_object {bucket_name} {object_name} succeed')
        return self.do_stat_object(bucket_name, object_name, alias=ROOT_ALIAS)

    def do_get_object(self, bucket_name:str, object_name:str, offset=0, length=0):
        client=Minio(self.url,access_key=ROOT_ACCESS_KEY,secret_key=ROOT_SECRET_KEY,secure=False)
        try:
            stat = client.stat_object(bucket_name, object_name)
            if stat.size == 0:
                offset = 0
            else:
                offset = offset % stat.size
            if length > stat.size - offset:
                length = stat.size - offset
            response = client.get_object(bucket_name, object_name, offset=offset, length=length)
            md5_hash = hashlib.md5()
            for data in response.stream(32*1024):
                md5_hash.update(data)
            md5_hex = md5_hash.hexdigest()
        except S3Error as e:
            return self.handleException(e, 'do_get_object', bucket_name=bucket_name, object_name=object_name, offset=offset, length=length)
        self.stats.success('do_get_object')
        self.logger.info(f'do_get_object {bucket_name} {object_name} succeed')
        return md5_hex

    def do_fput_object(self, bucket_name:str, object_name:str, src_path:str, alias):
        try:
            self.run_cmd(f'mc cp {src_path} {self.get_alias(alias)}/{bucket_name}/{object_name}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_fput_object', bucket_name=bucket_name, object_name=object_name, src_path=src_path)
        self.stats.success('do_fput_object')
        self.logger.info(f'do_fput_object {bucket_name} {object_name} {src_path} succeed')
        return self.do_stat_object(bucket_name, object_name, alias=ROOT_ALIAS)
    
    def do_fget_object(self, bucket_name:str, object_name:str, file_path:str, alias):
        try:
            self.run_cmd(f'mc cp {self.get_alias(alias)}/{bucket_name}/{object_name} {file_path}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_fget_object', bucket_name=bucket_name, object_name=object_name, file_path=file_path)
        self.stats.success('do_fget_object')
        self.logger.info(f'do_fget_object {bucket_name} {object_name} {file_path} succeed')
        return os.stat(file_path).st_size

    def object_exists(self, bucket_name:str, object_name:str, alias):
        try:
            self.run_cmd(f'mc stat {self.get_alias(alias)}/{bucket_name}/{object_name}')
        except subprocess.CalledProcessError as e:
            return False
        return True

    def do_remove_object(self, bucket_name:str, object_name:str, alias):
        try:
            self.run_cmd(f'mc rm {self.get_alias(alias)}/{bucket_name}/{object_name}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_remove_object', bucket_name=bucket_name, object_name=object_name)
        assert not self.object_exists(bucket_name, object_name, ROOT_ALIAS)
        self.stats.success('do_remove_object')
        self.logger.info(f'do_remove_object {bucket_name} {object_name} succeed')
        return True
    
    def do_list_objects(self, bucket_name, prefix, start_after, include_user_meta, include_version, use_url_encoding_type, recursive):
        client=Minio(self.url,access_key=ROOT_ACCESS_KEY,secret_key=ROOT_SECRET_KEY,secure=False)
        try:
            objects = client.list_objects(bucket_name, prefix=prefix, start_after=start_after, include_user_meta=include_user_meta, include_version=include_version, use_url_encoding_type=use_url_encoding_type, recursive=recursive)
        except S3Error as e:
            return self.handleException(e, 'do_list_objects', bucket_name=bucket_name, prefix=prefix, start_after=start_after, include_user_meta=include_user_meta, include_version=include_version, use_url_encoding_type=use_url_encoding_type, recursive=recursive)
        self.stats.success('do_list_objects')
        self.logger.info(f'do_list_objects {bucket_name} {prefix} {start_after} {include_user_meta} {include_version} {use_url_encoding_type} {recursive} succeed')
        result = '\n'.join([f'{obj.object_name} {obj.size} {obj.etag}' for obj in objects])
        return result
    
    def get_alias(self, alias):
        return self.prefix + '_' + alias

    def do_add_user(self, access_key, secret_key, alias):
        try:
            self.run_cmd(f'mc admin user add {self.get_alias(alias)} {access_key} {secret_key}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_add_user', access_key=access_key, secret_key=secret_key)
        self.stats.success('do_add_user')
        self.logger.info(f'do_add_user {access_key} succeed')
        return True
    
    def do_remove_user(self, access_key, alias):
        try:
            self.run_cmd(f'mc admin user remove {self.get_alias(alias)} {access_key}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_remove_user', access_key=access_key)
        self.stats.success('do_remove_user')
        self.logger.info(f'do_remove_user {access_key} succeed')
        return True

    def do_enable_user(self, access_key, alias):
        try:
            self.run_cmd(f'mc admin user enable {self.get_alias(alias)} {access_key}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_enable_user', access_key=access_key)
        self.stats.success('do_enable_user')
        self.logger.info(f'do_enable_user {access_key} succeed')
        return True
    
    def do_disable_user(self, access_key, alias):
        try:
            self.run_cmd(f'mc admin user disable {self.get_alias(alias)} {access_key}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_disable_user', access_key=access_key)
        self.stats.success('do_disable_user')
        self.logger.info(f'do_disable_user {access_key} succeed')
        return True
    
    def do_user_info(self, access_key, alias):
        try:
            self.run_cmd(f'mc admin user info {self.get_alias(alias)} {access_key}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_user_info', access_key=access_key)
        self.stats.success('do_user_info')
        self.logger.info(f'do_user_info {access_key} succeed')
        return True
    
    def do_list_users(self, alias):
        try:
            result = self.run_cmd(f'mc admin user list {self.get_alias(alias)}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_list_users')
        self.stats.success('do_list_users')
        self.logger.info(f'do_list_users succeed')
        return sorted(result.split("\n"))

    def remove_all_users(self, alias=ROOT_ALIAS):
        lines = self.run_cmd(f'mc admin user list {self.get_alias(alias)}').split("\n")
        for line in lines:
            if not line.strip():
                continue
            user = line.split()[1]
            self.run_cmd(f'mc admin user remove {self.get_alias(alias)} {user}')
            print(f"User '{user}' removed successfully.")

    def do_list_groups(self, alias):
        try:
            result = self.run_cmd(f'mc admin group list {self.get_alias(alias)}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_list_groups')
        self.stats.success('do_list_groups')
        self.logger.info(f'do_list_groups succeed')
        return sorted(result.split("\n"))

    def do_add_group(self, group_name, members, alias):
        try:
            self.run_cmd(f'mc admin group add {self.get_alias(alias)} {group_name} {" ".join(members)}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_add_group', group_name=group_name, members=members)
        self.stats.success('do_add_group')
        self.logger.info(f'do_add_group {group_name} {members} succeed')
        return self.do_group_info(group_name, alias)

    def do_remove_group(self, group_name, members, alias):
        try:
            self.run_cmd(f'mc admin group remove {self.get_alias(alias)} {group_name} {" ".join(members)}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_remove_group', group_name=group_name)
        self.stats.success('do_remove_group')
        self.logger.info(f'do_remove_group {group_name} succeed')
        return True

    def do_disable_group(self, group_name, alias):
        try:
            self.run_cmd(f'mc admin group disable {self.get_alias(alias)} {group_name}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_disable_group', group_name=group_name)
        self.stats.success('do_disable_group')
        self.logger.info(f'do_disable_group {group_name} succeed')
        return True
    
    def do_enable_group(self, group_name, alias):
        try:
            self.run_cmd(f'mc admin group enable {self.get_alias(alias)} {group_name}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_enable_group', group_name=group_name)
        self.stats.success('do_enable_group')
        self.logger.info(f'do_enable_group {group_name} succeed')
        return True

    def do_group_info(self, group_name, alias):
        try:
            self.run_cmd(f'mc admin group info {self.get_alias(alias)} {group_name}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_group_info', group_name=group_name)
        self.stats.success('do_group_info')
        self.logger.info(f'do_group_info {group_name} succeed')
        return True
    
    def remove_all_groups(self, alias=ROOT_ALIAS):
        groups = self.run_cmd(f'mc admin group list {self.get_alias(alias)}').split("\n")
        for group in groups:
            if not group.strip():
                continue
            self.run_cmd(f'mc admin group remove {self.get_alias(alias)} {group}')
            print(f"Group '{group}' removed successfully.")
    
    def do_add_policy(self, policy_name, policy_document, alias):
        policy = json.dumps(policy_document)
        print(policy)
        policy_path = 'policy.json'
        with open(policy_path, 'w') as f:
            f.write(policy)
        try:
            self.run_cmd(f'mc admin policy add {self.get_alias(alias)} {policy_name} {policy_path}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_add_policy', policy_name=policy_name)
        self.stats.success('do_add_policy')
        self.logger.info(f'do_add_policy {policy_name} succeed')
        return True
    
    def do_remove_policy(self, policy_name, alias):
        try:
            self.run_cmd(f'mc admin policy remove {self.get_alias(alias)} {policy_name}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_remove_policy', policy_name=policy_name)
        self.stats.success('do_remove_policy')
        self.logger.info(f'do_remove_policy {policy_name} succeed')
        return True
    
    def do_policy_info(self, policy_name, alias):
        try:
            result = self.run_cmd(f'mc admin policy info {self.get_alias(alias)} {policy_name}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_policy_info', policy_name=policy_name)
        self.stats.success('do_policy_info')
        self.logger.info(f'do_policy_info {policy_name} succeed')
        return self.sort_dict(json.loads(result))
    
    def do_list_policies(self, alias):
        try:
            result = self.run_cmd(f'mc admin policy list {self.get_alias(alias)}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_list_policies')
        self.stats.success('do_list_policies')
        self.logger.info(f'do_list_policies succeed')
        result = [item.strip() for item in result.split("\n") if item.strip()!='diagnostics' and item.strip()!='']
        return sorted(result)
    
    def remove_all_policies(self, alias=ROOT_ALIAS):
        policies = self.do_list_policies(alias)
        for policy in policies:
            if policy in BUILD_IN_POLICIES:
                continue
            self.run_cmd(f'mc admin policy remove {self.get_alias(alias)} {policy}')
            print(f"Policy '{policy}' removed successfully.")

    def do_set_policy_to_user(self, policy_name, user_name, alias):
        try:
            self.run_cmd(f'mc admin policy set {self.get_alias(alias)} {policy_name} user={user_name}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_set_policy_to_user', policy_name=policy_name, user_name=user_name)
        self.stats.success('do_set_policy_to_user')
        self.logger.info(f'do_set_policy_to_user {policy_name} {user_name} succeed')
        return True

    def do_set_policy_to_group(self, policy_name, group_name, alias):
        try:
            self.run_cmd(f'mc admin policy set {self.get_alias(alias)} {policy_name} group={group_name}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_set_policy_to_group', policy_name=policy_name, group_name=group_name)
        self.stats.success('do_set_policy_to_group')
        self.logger.info(f'do_set_policy_to_group {policy_name} {group_name} succeed')
        return True
    
    def do_unset_policy_from_user(self, policy_name, user_name, alias):
        try:
            self.run_cmd(f'mc admin policy unset {self.get_alias(alias)} {policy_name} user={user_name}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_unset_policy_from_user', policy_name=policy_name, user_name=user_name)
        self.stats.success('do_unset_policy_from_user')
        self.logger.info(f'do_unset_policy_from_user {policy_name} {user_name} succeed')
        return True
    
    def do_unset_policy_from_group(self, policy_name, group_name, alias):
        try:
            self.run_cmd(f'mc admin policy unset {self.get_alias(alias)} {policy_name} group={group_name}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_unset_policy_from_group', policy_name=policy_name, group_name=group_name)
        self.stats.success('do_unset_policy_from_group')
        self.logger.info(f'do_unset_policy_from_group {policy_name} {group_name} succeed')
        return True
    
    def do_set_alias(self, alias, access_key, secret_key, url):
        alias_name = self.get_alias(alias)
        try:
            self.run_cmd(f'mc alias set {alias_name} http://{url} {access_key} {secret_key}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_set_alias', alias=alias, url=url, access_key=access_key, secret_key=secret_key)
        self.stats.success('do_set_alias')
        self.logger.info(f'do_set_alias {alias} {url} {access_key} {secret_key} succeed')
        return True
    
    def do_remove_alias(self, alias):
        try:
            self.run_cmd(f'mc alias remove {self.get_alias(alias)}')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_remove_alias', alias=alias)
        self.stats.success('do_remove_alias')
        self.logger.info(f'do_remove_alias {alias} succeed')
        return True
    
    def do_list_aliases(self):
        try:
            result = self.run_cmd(f'mc alias list')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_list_aliases')
        self.stats.success('do_list_aliases')
        self.logger.info(f'do_list_aliases succeed')
        return sorted([line.strip() for line in result.split("\n") if line.strip() and ':' not in line])
    
    def remove_all_aliases(self):
        aliases = self.do_list_aliases()
        for alias in aliases:
            if alias.startswith(self.prefix+'_'):
                self.run_cmd(f'mc alias remove {alias}')
                print(f"Alias '{alias}' removed successfully.")