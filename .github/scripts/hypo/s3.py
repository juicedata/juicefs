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
# ./juicefs format sqlite3://test.db gateway
# MINIO_ROOT_USER=minioadmin MINIO_ROOT_PASSWORD=minioadmin ./juicefs gateway sqlite3://test.db localhost:9005 --multi-buckets --keep-etag

MAX_OBJECT_SIZE=10*1024*1024
# https://min.io/docs/minio/linux/administration/identity-access-management/policy-based-access-control.html#minio-policy-actions
S3_ACTION_LIST = ["s3:*", "s3:DeleteObject", "s3:GetObject","s3:ListBucket","s3:PutObject", "s3:PutObjectTagging", "s3:GetObjectTagging", "s3:DeleteObjectTagging"]
st_bucket_name = st.text(alphabet=ascii_lowercase, min_size=4, max_size=4)
st_object_name = st.text(alphabet=ascii_lowercase, min_size=4, max_size=4)
st_object_prefix = st.text(alphabet=ascii_lowercase, min_size=1, max_size=1)
st_content = st.binary(min_size=0, max_size=MAX_OBJECT_SIZE)
st_part_size = st.sampled_from([5*1024*1024, 8*1024*1024])
st_offset = st.integers(min_value=0, max_value=MAX_OBJECT_SIZE)
st_length = st.integers(min_value=0, max_value=MAX_OBJECT_SIZE)
st_policy = st.fixed_dictionaries({
    "Statement": st.lists(
        st.one_of(
            st.fixed_dictionaries({
                "Effect": st.sampled_from(["Allow", "Deny"]),
                "Principal": st.fixed_dictionaries({"AWS": st.just("*")}),
                "Resource": st.just("arn:aws:s3:::{{bucket}}"),
                "Action": st.lists(
                    st.sampled_from(["s3:GetBucketLocation", "s3:ListBucket"]),
                    min_size=1, max_size=3, 
                    unique=True
                ),
            }),
            st.fixed_dictionaries({
                "Effect": st.sampled_from(["Allow", "Deny"]),
                "Principal": st.fixed_dictionaries({"AWS": st.just("*")}),
                "Action": st.lists(
                    st.sampled_from(S3_ACTION_LIST),
                    min_size=1, max_size=3,
                    unique=True
                ),
                "Resource": st.just("arn:aws:s3:::{{bucket}}/*"),
            }),
        ),
        min_size=1, max_size=3
    )
})

SEED=int(os.environ.get('SEED', random.randint(0, 1000000000)))
@seed(SEED)
class S3Machine(RuleBasedStateMachine):
    buckets = Bundle('buckets')
    objects = Bundle('objects')
    BUCKET_NAME = 's3test'
    client1 = S3Client(name='minio', url='localhost:9000', access_key='minioadmin', secret_key='minioadmin')
    client2 = S3Client(name='juice', url='localhost:9005', access_key='minioadmin', secret_key='minioadmin')
    EXCLUDE_RULES = ['list_buckets']

    def __init__(self):
        super().__init__()
        self.client1.remove_all_buckets()
        self.client2.remove_all_buckets()

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
          prefix = st.one_of(st_object_prefix, None),
          start_after = st.one_of(st_object_name, None),
          include_user_meta = st.booleans(),
          include_version = st.booleans(),
          use_url_encoding_type = st.booleans(),
          recursive=st.booleans())
    @precondition(lambda self: 'list_objects' not in self.EXCLUDE_RULES)
    def list_objects(self, bucket_name, prefix=None, start_after=None, include_user_meta=False, include_version=False, use_url_encoding_type=True, recursive=False):
        result1 = self.client1.do_list_objects(bucket_name=bucket_name, prefix=prefix, start_after=start_after, include_user_meta=include_user_meta, include_version=include_version, use_url_encoding_type=use_url_encoding_type, recursive=recursive)
        result2 = self.client2.do_list_objects(bucket_name=bucket_name, prefix=prefix, start_after=start_after, include_user_meta=include_user_meta, include_version=include_version, use_url_encoding_type=use_url_encoding_type, recursive=recursive)
        assert self.equal(result1, result2), f'\033[31mlist_objects:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

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
        phases=[Phase.generate, Phase.target])
    profile = os.environ.get('PROFILE', 'dev')
    settings.load_profile(profile)
    
    s3machine = S3Machine.TestCase()
    s3machine.runTest()
    print(json.dumps(S3Client.stats.get(), sort_keys=True, indent=4))
    
    