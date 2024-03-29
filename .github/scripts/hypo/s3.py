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
import common
from common import run_cmd
from s3_op import S3Client

st_bucket_name = st.text(alphabet=ascii_lowercase, min_size=4, max_size=4)
st_object_name = st.text(alphabet=ascii_lowercase, min_size=4, max_size=4)
st_object_prefix = st.text(alphabet=ascii_lowercase, min_size=1, max_size=1)

SEED=int(os.environ.get('SEED', random.randint(0, 1000000000)))
# ./juicefs format sqlite3://test.db gateway
# MINIO_ROOT_USER=minioadmin MINIO_ROOT_PASSWORD=minioadmin ./juicefs gateway sqlite3://test.db localhost:9005 --multi-buckets --keep-etag
@seed(SEED)
class S3Machine(RuleBasedStateMachine):
    buckets = Bundle('buckets')
    objects = Bundle('objects')
    BUCKET_NAME = 's3test'
    client1 = S3Client(name='minio', url='localhost:9000', access_key='minioadmin', secret_key='minioadmin')
    client2 = S3Client(name='juice', url='localhost:9005', access_key='minioadmin', secret_key='minioadmin')
    EXCLUDE_RULES = []

    def __init__(self):
        super().__init__()
        self.client1.remove_all_buckets()
        self.client2.remove_all_buckets()

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
        bucket_name = consumes(buckets).filter(lambda x: x != multiple()))
    def remove_bucket(self, bucket_name):
        result1 = self.client1.do_remove_bucket(bucket_name)
        result2 = self.client2.do_remove_bucket(bucket_name)
        assert self.equal(result1, result2), f'\033[31mremove_bucket:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return bucket_name
        else:
            return multiple()

    @rule(
        target=objects,
        bucket_name = st.just(BUCKET_NAME),
        object_name = st_object_name)
    @precondition(lambda self: 'put_object' not in self.EXCLUDE_RULES)
    def put_object(self, bucket_name, object_name):
        result1 = self.client1.do_put_object(bucket_name, object_name, 'README.md')
        result2 = self.client2.do_put_object(bucket_name, object_name, 'README.md')
        assert self.equal(result1, result2), f'\033[31mput_object:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return multiple()
        else:
            return f'{bucket_name}:{object_name}'

    @rule(
        target = objects,
        obj = consumes(objects).filter(lambda x: x != multiple()))
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
            obj = objects.filter(lambda x: x != multiple())
          )
    @precondition(lambda self: 'stat_object' not in self.EXCLUDE_RULES)
    def stat_object(self, obj:str):
        bucket_name = obj.split(':')[0]
        object_name = obj.split(':')[1]
        result1 = self.client1.do_stat_object(bucket_name, object_name)
        result2 = self.client2.do_stat_object(bucket_name, object_name)
        assert self.equal(result1, result2), f'\033[31mstat_object:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(
          bucket_name = buckets.filter(lambda x: x != multiple()),
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
    profile = os.environ.get('PROFILE', 'dev')
    settings.load_profile(profile)
    
    s3machine = S3Machine.TestCase()
    s3machine.runTest()
    print(json.dumps(S3Client.stats.get(), sort_keys=True, indent=4))
    
    