import json
import os
import subprocess
try:
    __import__("hypothesis")
except ImportError:
    subprocess.check_call(["pip", "install", "hypothesis"])
from hypothesis import assume, settings, Verbosity
from hypothesis.stateful import rule, precondition, RuleBasedStateMachine, Bundle, initialize, multiple
from hypothesis import Phase, seed
import random
import common
from common import run_cmd
from strategy import *
from s3_op import S3Client

SEED=int(os.environ.get('SEED', random.randint(0, 1000000000)))
# ./juicefs format sqlite3://test.db gateway
# MINIO_ROOT_USER=minioadmin MINIO_ROOT_PASSWORD=minioadmin ./juicefs gateway sqlite3://test.db localhost:9005 --multi-buckets
@seed(SEED)
class S3Machine(RuleBasedStateMachine):
    BUCKET_NAME = 's3test'
    client1 = S3Client(name='minio', url='localhost:9000', access_key='minioadmin', secret_key='minioadmin')
    client2 = S3Client(name='juice', url='localhost:9005', access_key='minioadmin', secret_key='minioadmin')
    EXCLUDE_RULES = []

    def __init__(self):
        super().__init__()
        self.client1.do_create_bucket(self.BUCKET_NAME)
        self.client2.do_create_bucket(self.BUCKET_NAME)
        
    @rule()
    @precondition(lambda self: 'put_object' not in self.EXCLUDE_RULES)
    def put_object(self):
        self.client1.do_put_object(self.BUCKET_NAME, obj_name='test', src_path='README.md')
        self.client2.do_put_object(self.BUCKET_NAME, obj_name='test', src_path='README.md')

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
    
    