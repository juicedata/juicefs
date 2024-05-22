import unittest
from s3 import S3Machine
from s3_contant import *
class TestS3(unittest.TestCase):
    def test_bucket(self):
        state = S3Machine()
        state.set_alias('alias1', DEFAULT_ACCESS_KEY)
        state.create_bucket('bucket1')
        state.create_bucket('bucket2')
        state.fput_object('bucket1', 'object1', alias='alias1')
        state.fput_object('bucket1', 'object2', alias='alias1')
        state.fput_object('bucket2', 'object1', alias='alias1')
        state.fput_object('bucket2', 'object2', alias='alias1')
        state.list_buckets()
        state.list_objects('bucket1')
        state.list_objects('bucket2')
        state.list_objects('bucket1', prefix='obj')
        state.remove_object('bucket1:object1')
        state.remove_object('bucket1:object2')
        state.remove_bucket('bucket1')
        state.remove_bucket('bucket2')
        state.teardown()

    def test_user(self):
        state = S3Machine()
        state.create_bucket('bucket1')
        state.add_user('user1')
        state.add_user('user2')
        state.list_users()
        state.remove_user('user1')
        state.list_users()
        state.disable_user('user2')
        state.enable_user('user2')
        state.list_users()
        state.remove_user('user2')
        state.list_users()
        state.teardown()
        
    def test_group(self):
        state = S3Machine()
        state.create_bucket('bucket1')
        state.add_user('user1')
        state.add_user('user2')
        state.add_user('user3')
        state.add_group('group1', ['user1', 'user2'])
        state.add_group('group2', ['user2', 'user3'])
        state.list_groups()
        state.disable_group('group2')
        state.remove_group('group1', ['user1'])
        state.remove_group('group1', ['user2'])
        state.remove_group('group1', [])
        state.list_groups()
        state.enable_group('group2')
        state.list_groups()
        state.teardown()

    def skip_test_issue_4639(self):
        # SEE https://github.com/juicedata/juicefs/issues/4639
        state = S3Machine()
        v1 = state.init_aliases()
        v2, v3, v4, v5 = state.init_policies()
        state.remove_policy(alias=v1, policy_name=v3)
        state.list_groups(alias=v1)
        state.remove_policy(alias=v1, policy_name=v2)
        state.policy_info(alias=v1, policy_name=v5)
        state.teardown()

    def skip_test_issue_4660(self):
        #SEE https://github.com/juicedata/juicefs/issues/4660
        state = S3Machine()
        v1 = state.init_aliases()
        v2, v3, v4, v5 = state.init_policies()
        v8 = state.add_user(alias=v1, user_name='user1')
        state.disable_user(alias=v1, user_name=v8)
        state.set_alias(alias='pjzm', url1='localhost:9000', url2='localhost:9006', user_name=v8)
        state.teardown()

    def test_issue_4682(self):
        # SEE https://github.com/juicedata/juicefs/issues/4682
        state = S3Machine()
        v1 = state.init_aliases()
        v2, v3, v4, v5 = state.init_policies()
        v6 = state.create_bucket(alias=v1, bucket_name='nzpy')
        state.get_bucket_policy(alias=v1, bucket_name=v6)
        state.teardown()

    def test_xxx(self):
        state = S3Machine()
        policies_0, policies_1, policies_2, policies_3 = state.init_policies()
        aliases_0 = state.init_aliases()
        buckets_0 = state.create_bucket(alias=aliases_0, bucket_name='ohjf')
        objects_0 = state.fput_object(alias=aliases_0, bucket_name=buckets_0, object_name='tjex')
        state.stat_object(alias=aliases_0, obj=objects_0)
        state.list_groups(alias=aliases_0)
        state.stat_object(alias=aliases_0, obj=objects_0)
        state.list_groups(alias=aliases_0)
        policies_4 = state.add_policy(alias=aliases_0, policy_document={'Version': '2012-10-17',
        'Statement': [{'Effect': 'Allow',
        'Principal': {'AWS': '*'},
        'Action': ['s3:PutObject', 's3:GetObjectTagging', 's3:DeleteObject'],
        'Resource': 'arn:aws:s3:::*'},
        {'Effect': 'Deny',
        'Principal': {'AWS': '*'},
        'Action': ['s3:*', 's3:GetObject'],
        'Resource': 'arn:aws:s3:::*'}]}, policy_name='xiag')
        buckets_1 = state.create_bucket(alias=aliases_0, bucket_name='culn')
        buckets_2 = state.create_bucket(alias=aliases_0, bucket_name='piuk')
        state.set_alias(alias='vysh', url1='localhost:9000', url2='localhost:9005', user_name='user1')
        objects_1 = state.fput_object(alias=aliases_0, bucket_name=buckets_0, object_name='hlpf')
        state.stat_object(alias=aliases_0, obj=objects_0)
        objects_2 = state.fput_object(alias=aliases_0, bucket_name=buckets_2, object_name='hmkp')
        state.list_bucket_policy(alias=aliases_0, bucket_name=buckets_2, recursive=True)
        state.stat_object(alias=aliases_0, obj=objects_2)
        state.list_groups(alias=aliases_0)
        objects_3 = state.fput_object(alias=aliases_0, bucket_name=buckets_1, object_name='mnie')
        users_0 = state.add_user(alias=aliases_0, user_name='user2')
        state.remove_object(alias=aliases_0, obj=objects_1)
        policies_5 = state.add_policy(alias=aliases_0, policy_document={'Version': '2012-10-17',
        'Statement': [{'Effect': 'Deny',
        'Principal': {'AWS': '*'},
        'Action': ['s3:DeleteObjectTagging', 's3:GetObjectTagging', 's3:GetObject'],
        'Resource': 'arn:aws:s3:::*'},
        {'Effect': 'Allow',
        'Principal': {'AWS': '*'},
        'Action': ['s3:DeleteObjectTagging'],
        'Resource': 'arn:aws:s3:::*'},
        {'Effect': 'Deny',
        'Principal': {'AWS': '*'},
        'Action': ['s3:PutObjectTagging'],
        'Resource': 'arn:aws:s3:::*'}]}, policy_name='ilma')
        policies_6 = state.add_policy(alias=aliases_0, policy_document={'Version': '2012-10-17',
        'Statement': [{'Effect': 'Allow',
        'Principal': {'AWS': '*'},
        'Action': ['s3:DeleteObject'],
        'Resource': 'arn:aws:s3:::*'},
        {'Effect': 'Deny',
        'Principal': {'AWS': '*'},
        'Action': ['s3:DeleteObject', 's3:PutObject', 's3:GetObject'],
        'Resource': 'arn:aws:s3:::*'}]}, policy_name='nwfn')
        objects_4 = state.fput_object(alias=aliases_0, bucket_name=buckets_2, object_name='pcse')
        policies_7 = state.add_policy(alias=aliases_0, policy_document={'Version': '2012-10-17',
        'Statement': [{'Effect': 'Deny',
        'Principal': {'AWS': '*'},
        'Action': ['s3:*', 's3:PutObjectTagging', 's3:DeleteObjectTagging'],
        'Resource': 'arn:aws:s3:::*'}]}, policy_name='upfr')
        state.stat_object(alias=aliases_0, obj=objects_4)
        users_1 = state.add_user(alias=aliases_0, user_name='user3')
        users_2 = state.add_user(alias=aliases_0, user_name='user1')
        objects_5 = state.fput_object(alias=aliases_0, bucket_name=buckets_1, object_name='viwy')
        state.list_bucket_policy(alias=aliases_0, bucket_name=buckets_1, recursive=True)
        state.list_bucket_policy(alias=aliases_0, bucket_name=buckets_0, recursive=True)
        state.list_bucket_policy(alias=aliases_0, bucket_name=buckets_2, recursive=False)
        aliases_1 = state.set_alias(alias='zhxk', url1='localhost:9000', url2='localhost:9005', user_name=users_1)
        state.add_user(alias=aliases_1, user_name=users_2)
        state.stat_object(alias=aliases_1, obj=objects_2)
        objects_6 = state.fput_object(alias=aliases_0, bucket_name=buckets_2, object_name='udgt')
        state.list_groups(alias=aliases_1)
        state.remove_object(alias=aliases_0, obj=objects_6)
        objects_7 = state.remove_object(alias=aliases_1, obj=objects_0)
        state.create_bucket(alias=aliases_1, bucket_name='qqxv')
        state.add_user(alias=aliases_1, user_name=users_2)
        aliases_2 = state.set_alias(alias='oyfh', url1='localhost:9000', url2='localhost:9005', user_name=users_0)
        state.list_bucket_policy(alias=aliases_2, bucket_name=buckets_0, recursive=True)
        aliases_3 = state.set_alias(alias='zpdd', url1='localhost:9000', url2='localhost:9005', user_name=users_1)
        aliases_4 = state.set_alias(alias='irfh', url1='localhost:9000', url2='localhost:9005', user_name=users_2)
        policies_8 = state.add_policy(alias=aliases_0, policy_document={'Version': '2012-10-17',
        'Statement': [{'Effect': 'Deny',
        'Principal': {'AWS': '*'},
        'Action': ['s3:GetObjectTagging'],
        'Resource': 'arn:aws:s3:::*'},
        {'Effect': 'Deny',
        'Principal': {'AWS': '*'},
        'Action': ['s3:*', 's3:DeleteObjectTagging'],
        'Resource': 'arn:aws:s3:::*'},
        {'Effect': 'Allow',
        'Principal': {'AWS': '*'},
        'Action': ['s3:PutObject', 's3:ListBucket'],
        'Resource': 'arn:aws:s3:::*'}]}, policy_name='lpqo')
        state.stat_object(alias=aliases_0, obj=objects_2)
        state.stat_object(alias=aliases_2, obj=objects_2)
        aliases_5 = state.set_alias(alias='orop', url1='localhost:9000', url2='localhost:9005', user_name=users_0)
        aliases_6 = state.set_alias(alias='ovgg', url1='localhost:9000', url2='localhost:9005', user_name=users_0)
        state.stat_object(alias=aliases_2, obj=objects_4)
        state.stat_object(alias=aliases_5, obj=objects_7)
        state.teardown()
        
if __name__ == '__main__':
    unittest.main()