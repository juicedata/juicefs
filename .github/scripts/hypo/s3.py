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
from hypothesis.database import DirectoryBasedExampleDatabase
import random
from s3_op import S3Client
from s3_strategy import *
from s3_contant import *
import common

SEED=int(os.environ.get('SEED', random.randint(0, 1000000000)))
@seed(SEED)
class S3Machine(RuleBasedStateMachine):
    aliases = Bundle('aliases')
    buckets = Bundle('buckets')
    objects = Bundle('objects')
    users = Bundle('users')
    groups = Bundle('groups')
    policies = Bundle('policies')
    user_policies = Bundle('user_policy')
    group_policies = Bundle('group_policy')
    PREFIX1 = 'minio'
    PREFIX2 = 'juice'
    URL1 = 'localhost:9000'
    URL2 = 'localhost:9005'
    URL3 = 'localhost:9006'
    client1 = S3Client(prefix=PREFIX1, url=URL1)
    client2 = S3Client(prefix=PREFIX2, url=URL2, url2=URL3)
    EXCLUDE_RULES = []

    def __init__(self):
        super().__init__()
        self.client1.remove_all_aliases()
        self.client2.remove_all_aliases()
        self.client1.do_set_alias(ROOT_ALIAS, DEFAULT_ACCESS_KEY, DEFAULT_SECRET_KEY, self.URL1)
        self.client2.do_set_alias(ROOT_ALIAS, DEFAULT_ACCESS_KEY, DEFAULT_SECRET_KEY, self.URL2)
        self.client1.remove_all_buckets()
        self.client2.remove_all_buckets()
        self.client1.remove_all_users()
        self.client2.remove_all_users()
        self.client1.remove_all_groups()
        self.client2.remove_all_groups()
        self.client1.remove_all_policies()
        self.client2.remove_all_policies()

    @initialize(target=aliases)
    def init_aliases(self):
        return ROOT_ALIAS

    @initialize(target=policies)
    def init_policies(self):
        return multiple(*BUILD_IN_POLICIES)

    def equal(self, result1, result2):
        if os.getenv('PROFILE', 'dev') == 'generate':
            return True
        if type(result1) != type(result2):
            return False
        if isinstance(result1, Exception):
            result1 = str(result1)
            result2 = str(result2)
        result1 = common.replace(result1, self.PREFIX1, '***')
        result1 = common.replace(result1, self.URL1, '***')
        result2 = common.replace(result2, self.PREFIX2, '***')
        result2 = common.replace(result2, self.URL2, '***')
        # print(f'result1 is {result1}\nresult2 is {result2}')
        return result1 == result2

    @rule(alias = aliases)
    @precondition(lambda self: False)
    def info(self, alias=ROOT_ALIAS):
        result1 = self.client1.do_info(alias)
        result2 = self.client2.do_info(alias)
        assert self.equal(result1, result2), f'\033[31minfo:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(alias = aliases)
    @precondition(lambda self: 'list_buckets' not in self.EXCLUDE_RULES)
    def list_buckets(self, alias=ROOT_ALIAS):
        result1 = self.client1.do_list_buckets(alias)
        result2 = self.client2.do_list_buckets(alias)
        assert self.equal(result1, result2), f'\033[31mdo_list_buckets:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(
        target = buckets,
        alias = aliases,
        bucket_name = st_bucket_name)
    @precondition(lambda self: 'create_bucket' not in self.EXCLUDE_RULES)
    def create_bucket(self, bucket_name, alias = ROOT_ALIAS):
        result1 = self.client1.do_create_bucket(bucket_name, alias)
        result2 = self.client2.do_create_bucket(bucket_name, alias)
        assert self.equal(result1, result2), f'\033[31mcreate_bucket:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return multiple()
        else:
            return bucket_name
    @rule(
        target = buckets, 
        bucket_name = consumes(buckets),
        alias = aliases
    )
    @precondition(lambda self: 'remove_bucket' not in self.EXCLUDE_RULES)
    def remove_bucket(self, bucket_name, alias = ROOT_ALIAS):
        result1 = self.client1.do_remove_bucket(bucket_name, alias)
        result2 = self.client2.do_remove_bucket(bucket_name, alias)
        assert self.equal(result1, result2), f'\033[31mremove_bucket:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return bucket_name
        else:
            return multiple()

    @rule(
        alias = aliases,
        bucket_name = buckets.filter(lambda x: x != multiple()),
        policy = st.sampled_from(['public', 'download', 'upload', 'none'])
    )
    @precondition(lambda self: 'set_bucket_policy' not in self.EXCLUDE_RULES)
    def set_bucket_policy(self, bucket_name, policy, alias=ROOT_ALIAS):
        result1 = self.client1.do_set_bucket_policy(bucket_name, policy, alias)
        result2 = self.client2.do_set_bucket_policy(bucket_name, policy, alias)
        assert self.equal(result1, result2), f'\033[31mset_bucket_policy:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(
        bucket_name = buckets,
        alias = aliases
    )
    @precondition(lambda self: 'get_bucket_policy' not in self.EXCLUDE_RULES)
    def get_bucket_policy(self, bucket_name, alias=ROOT_ALIAS):
        result1 = self.client1.do_get_bucket_policy(bucket_name, alias)
        result2 = self.client2.do_get_bucket_policy(bucket_name, alias)
        assert self.equal(result1, result2), f'\033[31mget_bucket_policy:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(
        bucket_name = buckets,
        alias = aliases, 
        recursive = st.booleans()
    )
    def list_bucket_policy(self, bucket_name, alias=ROOT_ALIAS, recursive=False):
        result1 = self.client1.do_list_bucket_policy(bucket_name, alias, recursive)
        result2 = self.client2.do_list_bucket_policy(bucket_name, alias, recursive)
        assert self.equal(result1, result2), f'\033[31mlist_bucket_policy:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(
        target=objects,
        bucket_name = buckets,
        object_name = st_object_name, 
        data = st_content,
        use_part_size = st.booleans(),
        part_size = st_part_size
    )
    @precondition(lambda self: 'put_object' not in self.EXCLUDE_RULES)
    def put_object(self, bucket_name, object_name, data, use_part_size=False, part_size=5*1024*1024):
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
        alias = aliases,
        bucket_name = buckets,
        object_name = st_object_name)
    @precondition(lambda self: 'fput_object' not in self.EXCLUDE_RULES)
    def fput_object(self, bucket_name, object_name, alias=ROOT_ALIAS):
        result1 = self.client1.do_fput_object(bucket_name, object_name, 'README.md', alias)
        result2 = self.client2.do_fput_object(bucket_name, object_name, 'README.md', alias)
        assert self.equal(result1, result2), f'\033[31mfput_object:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return multiple()
        else:
            return f'{bucket_name}:{object_name}'

    @rule(
        obj = objects,
        alias = aliases,
        file_path = st.just('/tmp/file')
    )
    @precondition(lambda self: 'fget_object' not in self.EXCLUDE_RULES)
    def fget_object(self, obj:str, file_path, alias = ROOT_ALIAS):
        bucket_name = obj.split(':')[0]
        object_name = obj.split(':')[1]
        result1 = self.client1.do_fget_object(bucket_name, object_name, file_path, alias)
        result2 = self.client2.do_fget_object(bucket_name, object_name, file_path, alias)
        assert self.equal(result1, result2), f'\033[31mfget_object:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(
        target = objects,
        alias = aliases,
        obj = consumes(objects)
    )
    @precondition(lambda self: 'remove_object' not in self.EXCLUDE_RULES)
    def remove_object(self, obj:str, alias=ROOT_ALIAS):
        bucket_name = obj.split(':')[0]
        object_name = obj.split(':')[1]
        result1 = self.client1.do_remove_object(bucket_name, object_name, alias)
        result2 = self.client2.do_remove_object(bucket_name, object_name, alias)
        assert self.equal(result1, result2), f'\033[31mremove_object:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return obj
        else:
            return multiple()
        
    @rule(
        obj = objects, 
        alias = aliases
    )
    @precondition(lambda self: 'stat_object' not in self.EXCLUDE_RULES)
    def stat_object(self, obj:str, alias=ROOT_ALIAS):
        bucket_name = obj.split(':')[0]
        object_name = obj.split(':')[1]
        result1 = self.client1.do_stat_object(bucket_name, object_name, alias)
        result2 = self.client2.do_stat_object(bucket_name, object_name, alias)
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
        alias = aliases,
        user_name = st_user_name, 
    )
    @precondition(lambda self: 'add_user' not in self.EXCLUDE_RULES)
    def add_user(self, user_name, secret_key=DEFAULT_SECRET_KEY, alias = ROOT_ALIAS):
        result1 = self.client1.do_add_user(user_name, secret_key, alias)
        result2 = self.client2.do_add_user(user_name, secret_key, alias)
        assert self.equal(result1, result2), f'\033[31madd_user:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return multiple()
        else:
            return user_name
        
    @rule(
        target = users,
        alias = aliases,
        user_name = consumes(users).filter(lambda x: x != multiple())
    )
    @precondition(lambda self: 'remove_user' not in self.EXCLUDE_RULES)
    def remove_user(self, user_name, alias = ROOT_ALIAS):
        result1 = self.client1.do_remove_user(user_name, alias)
        result2 = self.client2.do_remove_user(user_name, alias)
        assert self.equal(result1, result2), f'\033[31mremove_user:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return user_name
        else:
            return multiple()

    @rule(
        alias = aliases,
        user_name = users.filter(lambda x: x != multiple())
    )
    @precondition(lambda self: 'enable_user' not in self.EXCLUDE_RULES)
    def enable_user(self, user_name, alias=ROOT_ALIAS):
        result1 = self.client1.do_enable_user(user_name, alias)
        result2 = self.client2.do_enable_user(user_name, alias)
        assert self.equal(result1, result2), f'\033[31menable_user:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(
        alias = aliases,
        user_name = users.filter(lambda x: x != multiple())
    )
    @precondition(lambda self: 'disable_user' not in self.EXCLUDE_RULES)
    def disable_user(self, user_name, alias=ROOT_ALIAS):
        result1 = self.client1.do_disable_user(user_name, alias)
        result2 = self.client2.do_disable_user(user_name, alias)
        assert self.equal(result1, result2), f'\033[31mdisable_user:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(
        alias = aliases,
        user_name = users.filter(lambda x: x != multiple())
    )
    @precondition(lambda self: 'user_info' not in self.EXCLUDE_RULES)
    def user_info(self, user_name, alias=ROOT_ALIAS):
        result1 = self.client1.do_user_info(user_name, alias)
        result2 = self.client2.do_user_info(user_name, alias)
        assert self.equal(result1, result2), f'\033[31muser_info:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(alias = aliases)
    @precondition(lambda self: 'list_users' not in self.EXCLUDE_RULES)
    def list_users(self, alias=ROOT_ALIAS):
        result1 = self.client1.do_list_users(alias)
        result2 = self.client2.do_list_users(alias)
        assert self.equal(result1, result2), f'\033[31mlist_users:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(alias = aliases)
    @precondition(lambda self: 'list_groups' not in self.EXCLUDE_RULES)
    def list_groups(self, alias=ROOT_ALIAS):
        result1 = self.client1.do_list_groups(alias)
        result2 = self.client2.do_list_groups(alias)
        assert self.equal(result1, result2), f'\033[31mlist_groups:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(
        target = groups,    
        alias = aliases,
        group_name=st_group_name, 
        members = st.lists(users, min_size=1, max_size=3)
    )
    @precondition(lambda self: 'add_group' not in self.EXCLUDE_RULES)
    def add_group(self, group_name, members, alias=ROOT_ALIAS):
        result1 = self.client1.do_add_group(group_name, members, alias)
        result2 = self.client2.do_add_group(group_name, members, alias)
        assert self.equal(result1, result2), f'\033[31madd_group:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return multiple()
        else:
            return group_name
        
    @rule(
        group_name = groups, 
        alias = aliases)
    @precondition(lambda self: 'group_info' not in self.EXCLUDE_RULES)
    def group_info(self, group_name, alias=ROOT_ALIAS):
        result1 = self.client1.do_group_info(group_name, alias)
        result2 = self.client2.do_group_info(group_name, alias)
        assert self.equal(result1, result2), f'\033[31mgroup_info:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
    
    @rule(
        target = groups,
        alias = aliases,
        group_name=consumes(groups).filter(lambda x: x != multiple()),
        group_members = st_group_members
    )
    @precondition(lambda self: 'remove_group' not in self.EXCLUDE_RULES)
    def remove_group(self, group_name, group_members, alias=ROOT_ALIAS):
        result1 = self.client1.do_remove_group(group_name, group_members, alias)
        result2 = self.client2.do_remove_group(group_name, group_members, alias)
        assert self.equal(result1, result2), f'\033[31mremove_group:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return group_name
        else:
            return multiple()
        
    @rule(
        alias = aliases,
        group_name=groups.filter(lambda x: x != multiple())
    )
    @precondition(lambda self: 'disable_group' not in self.EXCLUDE_RULES)
    def disable_group(self, group_name, alias=ROOT_ALIAS):
        result1 = self.client1.do_disable_group(group_name, alias)
        result2 = self.client2.do_disable_group(group_name, alias)
        assert self.equal(result1, result2), f'\033[31mdisable_group:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(
        alias = aliases,
        group_name=groups.filter(lambda x: x != multiple())
    )
    @precondition(lambda self: 'enable_group' not in self.EXCLUDE_RULES)
    def enable_group(self, group_name, alias=ROOT_ALIAS):
        result1 = self.client1.do_enable_group(group_name, alias)
        result2 = self.client2.do_enable_group(group_name, alias)
        assert self.equal(result1, result2), f'\033[31menable_group:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(
        target = policies,
        alias = st.just(ROOT_ALIAS),
        policy_name = st_policy_name,
        policy_document = st_policy
    )
    @precondition(lambda self: 'add_policy' not in self.EXCLUDE_RULES)
    def add_policy(self, policy_name, policy_document, alias=ROOT_ALIAS):
        result1 = self.client1.do_add_policy(policy_name, policy_document, alias)
        result2 = self.client2.do_add_policy(policy_name, policy_document, alias)
        assert self.equal(result1, result2), f'\033[31madd_policy:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return multiple()
        else:
            return policy_name
    
    @rule(
        target = policies,
        alias = st.just(ROOT_ALIAS),
        policy_name = consumes(policies).filter(lambda x: x != multiple()).filter(lambda x: x not in BUILD_IN_POLICIES)
    )
    @precondition(lambda self: 'remove_policy' not in self.EXCLUDE_RULES)
    def remove_policy(self, policy_name, alias=ROOT_ALIAS):
        assume(policy_name not in BUILD_IN_POLICIES)
        assert policy_name not in BUILD_IN_POLICIES, f'policy_name {policy_name} is in BUILD_IN_POLICIES'
        result1 = self.client1.do_remove_policy(policy_name, alias)
        result2 = self.client2.do_remove_policy(policy_name, alias)
        assert self.equal(result1, result2), f'\033[31mremove_policy:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return policy_name
        else:
            return multiple()

    @rule(
        alias = st.just(ROOT_ALIAS),
        policy_name = policies.filter(lambda x: x != multiple())
    )
    @precondition(lambda self: 'policy_info' not in self.EXCLUDE_RULES)
    def policy_info(self, policy_name, alias=ROOT_ALIAS):
        result1 = self.client1.do_policy_info(policy_name, alias)
        result2 = self.client2.do_policy_info(policy_name, alias)
        assert self.equal(result1, result2), f'\033[31mpolicy_info:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
    
    @rule(alias = st.just(ROOT_ALIAS))
    @precondition(lambda self: 'list_policies' not in self.EXCLUDE_RULES)
    def list_policies(self, alias=ROOT_ALIAS):
        result1 = self.client1.do_list_policies(alias)
        result2 = self.client2.do_list_policies(alias)
        assert self.equal(result1, result2), f'\033[31mlist_policies:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(
        target = user_policies,
        alias = st.just(ROOT_ALIAS),
        user_name = users.filter(lambda x: x != multiple()),
        policy_name = policies.filter(lambda x: x != multiple())
    )
    @precondition(lambda self: 'set_policy_to_user' not in self.EXCLUDE_RULES)
    def set_policy_to_user(self, policy_name, user_name, alias=ROOT_ALIAS):
        result1 = self.client1.do_set_policy_to_user(policy_name, user_name, alias)
        result2 = self.client2.do_set_policy_to_user(policy_name, user_name, alias)
        assert self.equal(result1, result2), f'\033[31mset_policy_to_user:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return multiple()
        else:
            return f'{user_name}:{policy_name}'

    @rule(
        target = group_policies, 
        alias = st.just(ROOT_ALIAS),
        group_name = groups.filter(lambda x: x != multiple()),
        policy_name = policies.filter(lambda x: x != multiple())
    )
    @precondition(lambda self: 'set_policy_to_group' not in self.EXCLUDE_RULES)
    def set_policy_to_group(self, group_name, policy_name, alias=ROOT_ALIAS):
        result1 = self.client1.do_set_policy_to_group(policy_name, group_name, alias)
        result2 = self.client2.do_set_policy_to_group(policy_name, group_name, alias)
        assert self.equal(result1, result2), f'\033[31mset_policy_to_group:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return multiple()
        else:
            return f'{group_name}:{policy_name}'
        
    @rule(
        target = user_policies,
        alias = st.just(ROOT_ALIAS),
        user_policy = consumes(user_policies).filter(lambda x: x != multiple())
    )
    @precondition(lambda self: 'unset_policy_from_user' not in self.EXCLUDE_RULES)
    def unset_policy_from_user(self, user_policy:str, alias=ROOT_ALIAS):
        user_name = user_policy.split(':')[0]
        policy_name = user_policy.split(':')[1]
        result1 = self.client1.do_unset_policy_from_user(policy_name, user_name, alias)
        result2 = self.client2.do_unset_policy_from_user(policy_name, user_name, alias)
        assert self.equal(result1, result2), f'\033[31munset_policy_from_user:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return user_policy
        else:
            return multiple()
        
    @rule(
        target = group_policies,
        alias = st.just(ROOT_ALIAS),
        group_policy = consumes(group_policies).filter(lambda x: x != multiple())
    )
    @precondition(lambda self: 'unset_policy_from_group' not in self.EXCLUDE_RULES)
    def unset_policy_from_group(self,  group_policy:str, alias=ROOT_ALIAS):
        group_name = group_policy.split(':')[0]
        policy_name = group_policy.split(':')[1]
        result1 = self.client1.do_unset_policy_from_group(policy_name, group_name, alias)
        result2 = self.client2.do_unset_policy_from_group(policy_name, group_name, alias)
        assert self.equal(result1, result2), f'\033[31munset_policy_from_group:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return group_policy
        else:
            return multiple()

    @rule(
        target=aliases, 
        alias = st_alias_name,
        user_name = st_user_name,
        url1=st.just(URL1),
        url2=st.sampled_from([URL2])
    )
    @precondition(lambda self: 'set_alias' not in self.EXCLUDE_RULES)
    def set_alias(self, alias, user_name, url1=URL1, url2=URL2):
        result1 = self.client1.do_set_alias(alias, user_name, DEFAULT_SECRET_KEY, url1)
        result2 = self.client2.do_set_alias(alias, user_name, DEFAULT_SECRET_KEY, url2)
        assert self.equal(result1, result2), f'\033[31mset_alias:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return multiple()
        else:
            return alias

    @rule(
        target = aliases,
        alias = consumes(aliases)
    )
    @precondition(lambda self: 'remove_alias' not in self.EXCLUDE_RULES)
    def remove_alias(self, alias):
        assume(alias != ROOT_ALIAS)
        result1 = self.client1.do_remove_alias(alias)
        result2 = self.client2.do_remove_alias(alias)
        assert self.equal(result1, result2), f'\033[31mremove_alias:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return alias
        else:
            return multiple()
    
    def teardown(self):
        pass

if __name__ == '__main__':
    MAX_EXAMPLE=int(os.environ.get('MAX_EXAMPLE', '100'))
    STEP_COUNT=int(os.environ.get('STEP_COUNT', '50'))
    ci_db = DirectoryBasedExampleDatabase(".hypothesis/examples") 
    settings.register_profile("dev", max_examples=MAX_EXAMPLE, verbosity=Verbosity.debug, 
        print_blob=True, stateful_step_count=STEP_COUNT, deadline=None, \
        report_multiple_bugs=False, 
        phases=[Phase.reuse, Phase.generate, Phase.target])
    settings.register_profile("schedule", max_examples=500, verbosity=Verbosity.debug, 
        print_blob=True, stateful_step_count=200, deadline=None, \
        report_multiple_bugs=False, 
        phases=[Phase.reuse, Phase.generate, Phase.target], 
        database=ci_db)
    settings.register_profile("pull_request", max_examples=100, verbosity=Verbosity.debug, 
        print_blob=True, stateful_step_count=30, deadline=None, \
        report_multiple_bugs=False, 
        phases=[Phase.reuse, Phase.generate, Phase.target], 
        database=ci_db)
    if os.environ.get('CI'):
        event_name = os.environ.get('GITHUB_EVENT_NAME')
        if event_name == 'schedule' or event_name == 'workflow_dispatch':
            profile = 'schedule'
        else:
            profile = 'pull_request'
    else:
        profile = os.environ.get('PROFILE', 'dev')
    print(f'profile is {profile}')
    settings.load_profile(profile)
    
    s3machine = S3Machine.TestCase()
    s3machine.runTest()
    print(json.dumps(S3Client.stats.get(), sort_keys=True, indent=4))
    
    