import json
import os
from string import ascii_lowercase
import subprocess
try:
    __import__("hypothesis")
except ImportError:
    subprocess.check_call(["pip", "install", "hypothesis"])
from hypothesis import assume, settings, Verbosity
from hypothesis.stateful import rule, precondition, RuleBasedStateMachine, Bundle, initialize, multiple, consumes
from hypothesis import Phase, seed
from hypothesis import strategies as st
import random
from s3_op import S3Client
from s3_strategy import *
# ./juicefs format sqlite3://test.db gateway
# MINIO_ROOT_USER=minioadmin MINIO_ROOT_PASSWORD=minioadmin ./juicefs gateway sqlite3://test.db localhost:9005 --multi-buckets --keep-etag

SEED=int(os.environ.get('SEED', random.randint(0, 1000000000)))
@seed(SEED)
class S3Machine(RuleBasedStateMachine):
    buckets = Bundle('buckets')
    objects = Bundle('objects')
    users = Bundle('users')
    groups = Bundle('groups')
    policies = Bundle('policies')
    user_policies = Bundle('user_policy')
    group_policies = Bundle('group_policy')

    BUCKET_NAME = 's3test'
    client1 = S3Client(alias='minio', url='localhost:9000', access_key='minioadmin', secret_key='minioadmin')
    client2 = S3Client(alias='juice', url='localhost:9005', access_key='minioadmin', secret_key='minioadmin')
    EXCLUDE_RULES = []

    def __init__(self):
        super().__init__()
        self.client1.remove_all_buckets()
        self.client2.remove_all_buckets()
        self.client1.remove_all_users()
        self.client2.remove_all_users()
        self.client1.remove_all_groups()
        self.client2.remove_all_groups()

    @initialize(target=policies)
    def init_policies(self):
        return multiple('consoleAdmin', 'readonly', 'readwrite', 'diagnostics', 'writeonly')

    # @initialize(target=buckets)
    # def init_buckets(self):
    #     self.client1.do_create_bucket(self.BUCKET_NAME)
    #     self.client2.do_create_bucket(self.BUCKET_NAME)
    #     return self.BUCKET_NAME
    
    def equal(self, result1, result2):
        if os.getenv('PROFILE', 'dev') == 'generate':
            return True
        if type(result1) != type(result2):
            return False
        if isinstance(result1, Exception):
            r1 = str(result1)
            r2 = str(result2)
            return r1 == r2
        elif isinstance(result1, tuple):
            return result1 == result2
        else:
            return result1 == result2

    @rule()
    @precondition(lambda self: 'info' not in self.EXCLUDE_RULES)
    def info(self):
        result1 = self.client1.do_info()
        result2 = self.client2.do_info()
        assert self.equal(result1, result2), f'\033[31minfo:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule()
    @precondition(lambda self: 'list_buckets' not in self.EXCLUDE_RULES)
    def list_buckets(self):
        result1 = self.client1.do_list_buckets()
        result2 = self.client2.do_list_buckets()
        assert self.equal(result1, result2), f'\033[31mdo_list_buckets:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(
        target = buckets,
        bucket_name = st_bucket_name)
    @precondition(lambda self: 'create_bucket' not in self.EXCLUDE_RULES)
    def create_bucket(self, bucket_name):
        result1 = self.client1.do_create_bucket(bucket_name)
        result2 = self.client2.do_create_bucket(bucket_name)
        assert self.equal(result1, result2), f'\033[31mcreate_bucket:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return multiple()
        else:
            return bucket_name
    @rule(
        target = buckets, 
        bucket_name = consumes(buckets)
    )
    @precondition(lambda self: 'remove_bucket' not in self.EXCLUDE_RULES)
    def remove_bucket(self, bucket_name):
        result1 = self.client1.do_remove_bucket(bucket_name)
        result2 = self.client2.do_remove_bucket(bucket_name)
        assert self.equal(result1, result2), f'\033[31mremove_bucket:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return bucket_name
        else:
            return multiple()

    @rule(
        bucket_name = buckets.filter(lambda x: x != multiple()),
        policy = st_policy
    )
    @precondition(lambda self: 'set_bucket_policy' not in self.EXCLUDE_RULES)
    def set_bucket_policy(self, bucket_name, policy):
        policy_str = json.dumps(policy).replace('{{bucket}}', bucket_name)
        result1 = self.client1.do_set_bucket_policy(bucket_name, policy_str)
        result2 = self.client2.do_set_bucket_policy(bucket_name, policy_str)
        assert self.equal(result1, result2), f'\033[31mset_bucket_policy:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(
        bucket_name = buckets
    )
    @precondition(lambda self: 'get_bucket_policy' not in self.EXCLUDE_RULES)
    def get_bucket_policy(self, bucket_name):
        result1 = self.client1.do_get_bucket_policy(bucket_name)
        result2 = self.client2.do_get_bucket_policy(bucket_name)
        assert self.equal(result1, result2), f'\033[31mget_bucket_policy:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(
        bucket_name = buckets
    )
    @precondition(lambda self: 'delete_bucket_policy' not in self.EXCLUDE_RULES)
    def delete_bucket_policy(self, bucket_name):
        result1 = self.client1.do_delete_bucket_policy(bucket_name)
        result2 = self.client2.do_delete_bucket_policy(bucket_name)
        assert self.equal(result1, result2), f'\033[31mdelete_bucket_policy:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(
        target=objects,
        bucket_name = buckets,
        object_name = st_object_name, 
        data = st_content,
        use_part_size = st.booleans(),
        part_size = st_part_size
    )
    @precondition(lambda self: 'put_object' not in self.EXCLUDE_RULES)
    def put_object(self, bucket_name, object_name, data, use_part_size, part_size=5*1024*1024):
        if use_part_size:
            result1 = self.client1.do_put_object(bucket_name, object_name, data, -1, part_size=part_size)
            result2 = self.client2.do_put_object(bucket_name, object_name, data, -1, part_size=part_size)
        else:
            result1 = self.client1.do_put_object(bucket_name, object_name, data, len(data))
            result2 = self.client2.do_put_object(bucket_name, object_name, data, len(data))
        assert self.equal(result1, result2), f'\033[31mput_object:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return multiple()
        else:
            return f'{bucket_name}:{object_name}'

    @rule(
        obj = objects,
        offset = st_offset, 
        length = st_length
    )
    @precondition(lambda self: 'get_object' not in self.EXCLUDE_RULES)
    def get_object(self, obj:str, offset=0, length=0):
        bucket_name = obj.split(':')[0]
        object_name = obj.split(':')[1]
        result1 = self.client1.do_get_object(bucket_name, object_name, offset=offset, length=length)
        result2 = self.client2.do_get_object(bucket_name, object_name, offset=offset, length=length)
        assert self.equal(result1, result2), f'\033[31mget_object:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(
        target=objects,
        bucket_name = buckets,
        object_name = st_object_name)
    @precondition(lambda self: 'fput_object' not in self.EXCLUDE_RULES)
    def fput_object(self, bucket_name, object_name):
        result1 = self.client1.do_fput_object(bucket_name, object_name, 'README.md')
        result2 = self.client2.do_fput_object(bucket_name, object_name, 'README.md')
        assert self.equal(result1, result2), f'\033[31mfput_object:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return multiple()
        else:
            return f'{bucket_name}:{object_name}'

    @rule(
        obj = objects,
        file_path = st.just('/tmp/file')
    )
    @precondition(lambda self: 'fget_object' not in self.EXCLUDE_RULES)
    def fget_object(self, obj:str, file_path):
        bucket_name = obj.split(':')[0]
        object_name = obj.split(':')[1]
        result1 = self.client1.do_fget_object(bucket_name, object_name, file_path)
        result2 = self.client2.do_fget_object(bucket_name, object_name, file_path)
        assert self.equal(result1, result2), f'\033[31mfget_object:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(
        target = objects,
        obj = consumes(objects)
    )
    @precondition(lambda self: 'remove_object' not in self.EXCLUDE_RULES)
    def remove_object(self, obj:str):
        bucket_name = obj.split(':')[0]
        object_name = obj.split(':')[1]
        result1 = self.client1.do_remove_object(bucket_name, object_name)
        result2 = self.client2.do_remove_object(bucket_name, object_name)
        assert self.equal(result1, result2), f'\033[31mremove_object:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return object_name
        else:
            return multiple()
        
    @rule(
        obj = objects
    )
    @precondition(lambda self: 'stat_object' not in self.EXCLUDE_RULES)
    def stat_object(self, obj:str):
        bucket_name = obj.split(':')[0]
        object_name = obj.split(':')[1]
        result1 = self.client1.do_stat_object(bucket_name, object_name)
        result2 = self.client2.do_stat_object(bucket_name, object_name)
        assert self.equal(result1, result2), f'\033[31mstat_object:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(
          bucket_name = buckets,
          prefix = st.just(None), # st.one_of(st_object_prefix, st.just(None)),
          start_after = st.one_of(st_object_name, st.just(None)),
          include_user_meta = st.booleans(),
          include_version = st.just(False),
          use_url_encoding_type = st.booleans(),
          recursive=st.booleans())
    @precondition(lambda self: 'list_objects' not in self.EXCLUDE_RULES)
    def list_objects(self, bucket_name, prefix=None, start_after=None, include_user_meta=False, include_version=False, use_url_encoding_type=True, recursive=False):
        result1 = self.client1.do_list_objects(bucket_name=bucket_name, prefix=prefix, start_after=start_after, include_user_meta=include_user_meta, include_version=include_version, use_url_encoding_type=use_url_encoding_type, recursive=recursive)
        result2 = self.client2.do_list_objects(bucket_name=bucket_name, prefix=prefix, start_after=start_after, include_user_meta=include_user_meta, include_version=include_version, use_url_encoding_type=use_url_encoding_type, recursive=recursive)
        assert self.equal(result1, result2), f'\033[31mlist_objects:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(
        target = users,
        user_name = st_user_name, 
        secret_key = st_secret_key
    )
    @precondition(lambda self: 'add_user' not in self.EXCLUDE_RULES)
    def add_user(self, user_name, secret_key='minioadmin'):
        result1 = self.client1.do_add_user(user_name, secret_key)
        result2 = self.client2.do_add_user(user_name, secret_key)
        assert self.equal(result1, result2), f'\033[31madd_user:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return multiple()
        else:
            return user_name
        
    @rule(
        target = users,
        user_name = consumes(users).filter(lambda x: x != multiple())
    )
    @precondition(lambda self: 'remove_user' not in self.EXCLUDE_RULES)
    def remove_user(self, user_name):
        result1 = self.client1.do_remove_user(user_name)
        result2 = self.client2.do_remove_user(user_name)
        assert self.equal(result1, result2), f'\033[31mremove_user:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return user_name
        else:
            return multiple()

    @rule(
        user_name = users.filter(lambda x: x != multiple())
    )
    @precondition(lambda self: 'enable_user' not in self.EXCLUDE_RULES)
    def enable_user(self, user_name):
        result1 = self.client1.do_enable_user(user_name)
        result2 = self.client2.do_enable_user(user_name)
        assert self.equal(result1, result2), f'\033[31menable_user:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(
        user_name = users.filter(lambda x: x != multiple())
    )
    @precondition(lambda self: 'disable_user' not in self.EXCLUDE_RULES)
    def disable_user(self, user_name):
        result1 = self.client1.do_disable_user(user_name)
        result2 = self.client2.do_disable_user(user_name)
        assert self.equal(result1, result2), f'\033[31mdisable_user:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(
        user_name = users.filter(lambda x: x != multiple())
    )
    @precondition(lambda self: 'user_info' not in self.EXCLUDE_RULES)
    def user_info(self, user_name):
        result1 = self.client1.do_user_info(user_name)
        result2 = self.client2.do_user_info(user_name)
        assert self.equal(result1, result2), f'\033[31muser_info:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(
    )
    @precondition(lambda self: 'list_users' not in self.EXCLUDE_RULES)
    def list_users(self):
        result1 = self.client1.do_list_users()
        result2 = self.client2.do_list_users()
        assert self.equal(result1, result2), f'\033[31mlist_users:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule()
    @precondition(lambda self: 'list_groups' not in self.EXCLUDE_RULES)
    def list_groups(self):
        result1 = self.client1.do_list_groups()
        result2 = self.client2.do_list_groups()
        assert self.equal(result1, result2), f'\033[31mlist_groups:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(
        target = groups,    
        group_name=st_group_name, 
        members = st_group_members)
    @precondition(lambda self: 'add_group' not in self.EXCLUDE_RULES)
    def add_group(self, group_name, members):
        result1 = self.client1.do_add_group(group_name, members)
        result2 = self.client2.do_add_group(group_name, members)
        assert self.equal(result1, result2), f'\033[31madd_group:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return multiple()
        else:
            return group_name
        
    @rule(
        target = groups,
        group_name=consumes(groups).filter(lambda x: x != multiple())
    )
    @precondition(lambda self: 'remove_group' not in self.EXCLUDE_RULES)
    def remove_group(self, group_name):
        result1 = self.client1.do_remove_group(group_name)
        result2 = self.client2.do_remove_group(group_name)
        assert self.equal(result1, result2), f'\033[31mremove_group:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return group_name
        else:
            return multiple()
        
    @rule(
        group_name=groups.filter(lambda x: x != multiple())
    )
    @precondition(lambda self: 'disable_group' not in self.EXCLUDE_RULES)
    def disable_group(self, group_name):
        result1 = self.client1.do_disable_group(group_name)
        result2 = self.client2.do_disable_group(group_name)
        assert self.equal(result1, result2), f'\033[31mdisable_group:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(
        group_name=groups.filter(lambda x: x != multiple())
    )
    @precondition(lambda self: 'enable_group' not in self.EXCLUDE_RULES)
    def enable_group(self, group_name):
        result1 = self.client1.do_enable_group(group_name)
        result2 = self.client2.do_enable_group(group_name)
        assert self.equal(result1, result2), f'\033[31menable_group:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(
        target = policies,
        policy_name = st_policy_name,
        policy = st_policy
    )
    @precondition(lambda self: 'create_policy' not in self.EXCLUDE_RULES)
    def create_policy(self, policy_name, policy):
        #TODO: render policy with bucket name
        policy_str = json.dumps(policy)
        result1 = self.client1.do_create_policy(policy_name, policy_str)
        result2 = self.client2.do_create_policy(policy_name, policy_str)
        assert self.equal(result1, result2), f'\033[31m_create_policy:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return multiple()
        else:
            return policy_name
    
    @rule(
        target = policies,
        policy_name = consumes(policies).filter(lambda x: x != multiple())
    )
    @precondition(lambda self: 'remove_policy' not in self.EXCLUDE_RULES)
    def remove_policy(self, policy_name):
        result1 = self.client1.do_remove_policy(policy_name)
        result2 = self.client2.do_remove_policy(policy_name)
        assert self.equal(result1, result2), f'\033[31mremove_policy:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return policy_name
        else:
            return multiple()

    @rule(
        policy_name = policies.filter(lambda x: x != multiple())
    )
    @precondition(lambda self: 'policy_info' not in self.EXCLUDE_RULES)
    def policy_info(self, policy_name):
        result1 = self.client1.do_policy_info(policy_name)
        result2 = self.client2.do_policy_info(policy_name)
        assert self.equal(result1, result2), f'\033[31mpolicy_info:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
    
    @rule()
    @precondition(lambda self: 'list_policies' not in self.EXCLUDE_RULES)
    def list_policies(self):
        result1 = self.client1.do_list_policies()
        result2 = self.client2.do_list_policies()
        assert self.equal(result1, result2), f'\033[31mlist_policies:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(
        target = user_policies,
        user_name = users.filter(lambda x: x != multiple()),
        policy_name = policies.filter(lambda x: x != multiple())
    )
    @precondition(lambda self: 'attach_policy_to_user' not in self.EXCLUDE_RULES)
    def attach_policy_to_user(self, user_name, policy_name):
        result1 = self.client1.do_attach_policy_to_user(policy_name, user_name)
        result2 = self.client2.do_attach_policy_to_user(policy_name, user_name)
        assert self.equal(result1, result2), f'\033[31mattach_policy_to_user:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return multiple()
        else:
            return f'{user_name}:{policy_name}'

    @rule(
        target = group_policies, 
        group_name = groups.filter(lambda x: x != multiple()),
        policy_name = policies.filter(lambda x: x != multiple())
    )
    @precondition(lambda self: 'attach_policy_to_group' not in self.EXCLUDE_RULES)
    def attach_policy_to_group(self, group_name, policy_name):
        result1 = self.client1.do_attach_policy_to_group(policy_name, group_name)
        result2 = self.client2.do_attach_policy_to_group(policy_name, group_name)
        assert self.equal(result1, result2), f'\033[31mattach_policy_to_group:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return multiple()
        else:
            return f'{group_name}:{policy_name}'
        
    @rule(
        target = user_policies,
        user_policy = consumes(user_policies).filter(lambda x: x != multiple())
    )
    @precondition(lambda self: 'detach_policy_from_user' not in self.EXCLUDE_RULES)
    def detach_policy_from_user(self, user_policy):
        user_name = user_policy.split(':')[0]
        policy_name = user_policy.split(':')[1]
        result1 = self.client1.do_detach_policy_from_user(policy_name, user_name)
        result2 = self.client2.do_detach_policy_from_user(policy_name, user_name)
        assert self.equal(result1, result2), f'\033[31mdetach_policy_to_user:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return user_policy
        else:
            return multiple()
        
    @rule(
        target = group_policies,
        group_policy = consumes(group_policies).filter(lambda x: x != multiple())
    )
    @precondition(lambda self: 'detach_policy_from_group' not in self.EXCLUDE_RULES)
    def detach_policy_from_group(self, group_policy):
        group_name = group_policy.split(':')[0]
        policy_name = group_policy.split(':')[1]
        result1 = self.client1.do_detach_policy_from_group(policy_name, group_name)
        result2 = self.client2.do_detach_policy_from_group(policy_name, group_name)
        assert self.equal(result1, result2), f'\033[31mdetach_policy_to_group:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return group_policy
        else:
            return multiple()

    def teardown(self):
        pass

if __name__ == '__main__':
    MAX_EXAMPLE=int(os.environ.get('MAX_EXAMPLE', '100'))
    STEP_COUNT=int(os.environ.get('STEP_COUNT', '50'))
    settings.register_profile("dev", max_examples=MAX_EXAMPLE, verbosity=Verbosity.debug, 
        print_blob=True, stateful_step_count=STEP_COUNT, deadline=None, \
        report_multiple_bugs=False, 
        phases=[Phase.reuse, Phase.generate, Phase.target, Phase.shrink, Phase.explain])
    settings.register_profile("dev", max_examples=MAX_EXAMPLE, verbosity=Verbosity.debug, 
        print_blob=True, stateful_step_count=STEP_COUNT, deadline=None, \
        report_multiple_bugs=False, 
        phases=[Phase.generate])
    profile = os.environ.get('PROFILE', 'dev')
    settings.load_profile(profile)
    
    s3machine = S3Machine.TestCase()
    s3machine.runTest()
    print(json.dumps(S3Client.stats.get(), sort_keys=True, indent=4))
    
    