import unittest
from s3 import S3Machine

class TestS3(unittest.TestCase):
    def test_bucket(self):
        state = S3Machine()
        state.create_bucket('bucket1')
        state.create_bucket('bucket2')
        state.fput_object('bucket1', 'object1')
        state.fput_object('bucket1', 'object2')
        state.fput_object('bucket2', 'object1')
        state.fput_object('bucket2', 'object2')
        state.list_buckets()
        state.list_objects('bucket1')
        state.list_objects('bucket2')
        state.list_objects('bucket1', prefix='obj')
        state.remove_object('bucket1:object1')
        state.remove_object('bucket1:object2')
        state.remove_bucket('bucket1')
        state.remove_bucket('bucket2')
        state.teardown()

    def test_s3_2(self):
        state = S3Machine()
        v1 = state.create_bucket(bucket_name='lwre')
        v2 = state.create_bucket(bucket_name='imrr')
        v3 = state.fput_object(bucket_name=v1, object_name='zqqs')
        state.get_bucket_policy(bucket_name=v1)
        state.delete_bucket_policy(bucket_name=v2)
        state.put_object(bucket_name=v1, data=b'\x1c', object_name='mvtl', part_size=8388608, use_part_size=False)
        state.teardown()

    def test_policy(self):
        state = S3Machine()
        state.create_bucket('bucket1')
        state.add_user('user1')
        state.add_policy(policy_name='policy1', policy_document={
                "Version" : "2012-10-17",
                'Statement': [{
                    'Effect': 'Deny',
                    'Principal': {'AWS': '*'},
                    'Action': ['s3:PutObject'],
                    'Resource': 'arn:aws:s3:::bucket1/*'
                }]
            }
        )
        state.set_policy_to_user('policy1', 'user1')
        state.put_object('bucket1', 'object1', data=b'hello')
        state.add_policy(policy_name='policy2', policy_document={
                "Version" : "2012-10-17",
                'Statement': [{
                    'Effect': 'Allow',
                    'Principal': {'AWS': '*'},
                    'Action': ['s3:GetObject'],
                    'Resource': 'arn:aws:s3:::bucket1/*'
                }]
            }
        )
        state.get_object('bucket1:object1')

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
        state.remove_group('group1')
        state.list_groups()
        state.enable_group('group2')
        state.list_groups()
        state.teardown()

    def test_policy_remove(self):
        state = S3Machine()
        v1 = state.init_aliases()
        v2, v3, v4, v5 = state.init_policies()
        state.remove_policy(alias=v1, policy_name=v3)
        state.list_groups(alias=v1)
        state.remove_policy(alias=v1, policy_name=v2)
        state.policy_info(alias=v1, policy_name=v5)
        state.teardown()

    def test_policy(self):
        state = S3Machine()
        v1, v2, v3, v4, v5 = state.init_policies()
        v6 = state.init_aliases()
        state.list_buckets()
        state.list_users(alias=v6)
        state.list_users(alias=v6)
        state.list_buckets()
        state.add_policy(alias=v6, policy_document={'Statement': [{'Effect': 'Deny',
        'Principal': {'AWS': '*'},
        'Action': ['s3:GetObject'],
        'Resource': 'arn:aws:s3:::*'}]}, policy_name='nufi')
        state.policy_info(alias=v6, policy_name=v3)
        state.add_policy(alias=v6, policy_document={'Statement': [{'Effect': 'Allow',
        'Principal': {'AWS': '*'},
        'Action': ['s3:GetObjectTagging',
            's3:DeleteObjectTagging',
            's3:ListBucket'],
        'Resource': 'arn:aws:s3:::*'},
        {'Effect': 'Allow',
        'Principal': {'AWS': '*'},
        'Action': ['s3:ListBucket'],
        'Resource': 'arn:aws:s3:::*'},
        {'Effect': 'Allow',
        'Principal': {'AWS': '*'},
        'Resource': 'arn:aws:s3:::*',
        'Action': ['s3:GetBucketLocation']}]}, policy_name='eqru')
        v7 = state.add_user(alias=v6, secret_key='fzydbhsl', user_name='user1')
        state.list_aliases()
        v8 = state.create_bucket(bucket_name='bwsl')
        state.list_policies(alias=v6)
        state.teardown()

    def test_policy2(self):
        state = S3Machine()
        v1, v2, v3, v4, v5 = state.init_policies()
        v6 = state.init_aliases()
        state.list_groups(alias=v6)
        state.add_policy(alias=v6, policy_document={'Statement': [{'Effect': 'Allow',
        'Principal': {'AWS': '*'},
        'Resource': 'arn:aws:s3:::*',
        'Action': ['s3:ListBucket', 's3:GetBucketLocation']}]}, policy_name='klvk')
        v7 = state.add_user(alias=v6, secret_key='uacoajrw', user_name='user3')
        state.policy_info(alias=v6, policy_name=v1)
        state.teardown()

    def test_policy3(self):
        state = S3Machine()
        v1 = state.init_aliases()
        v2, v3, v4, v5 = state.init_policies()
        state.list_aliases()
        state.list_groups(alias=v1)
        state.list_aliases()
        state.list_groups(alias=v1)
        state.list_users(alias=v1)
        state.list_aliases()
        state.list_groups(alias=v1)
        state.list_buckets()
        state.list_aliases()
        state.policy_info(alias=v1, policy_name=v4)
        state.teardown()

    def test_policy4(self):
        state = S3Machine()
        v1 = state.init_aliases()
        v2, v3, v4, v5, v6 = state.init_policies()
        state.list_buckets()
        state.list_aliases()
        state.list_groups(alias=v1)
        v7 = state.add_user(alias=v1, secret_key='njqyqxnp', user_name='user3')
        state.set_alias(alias='qmbk', user_name=v7)
        v8 = state.add_user(alias=v1, secret_key='hampoewy', user_name='user1')
        state.list_groups(alias=v1)
        state.add_policy(alias=v1, policy_document={'Statement': [{'Effect': 'Allow',
        'Principal': {'AWS': '*'},
        'Action': ['s3:PutObjectTagging', 's3:DeleteObject'],
        'Resource': 'arn:aws:s3:::*'},
        {'Effect': 'Allow',
        'Principal': {'AWS': '*'},
        'Resource': 'arn:aws:s3:::*',
        'Action': ['s3:ListBucket']},
        {'Effect': 'Deny',
        'Principal': {'AWS': '*'},
        'Action': ['s3:PutObject', 's3:DeleteObject', 's3:*'],
        'Resource': 'arn:aws:s3:::*'}]}, policy_name='yekr')
        state.list_buckets()
        state.list_buckets()
        state.set_alias(alias='eptf', user_name='user2')
        state.list_groups(alias=v1)
        state.list_buckets()
        state.disable_user(alias=v1, user_name=v7)
        state.list_aliases()
        state.list_buckets()
        v9 = state.add_user(alias=v1, secret_key='hfiwfzcu', user_name=v8)
        state.list_aliases()
        state.set_alias(alias='rjhj', user_name='user2')
        v10 = state.add_user(alias=v1, secret_key='xvuvuatt', user_name=v8)
        state.list_groups(alias=v1)
        state.disable_user(alias=v1, user_name=v8)
        state.list_aliases()
        state.add_policy(alias=v1, policy_document={'Statement': [{'Effect': 'Allow',
        'Principal': {'AWS': '*'},
        'Resource': 'arn:aws:s3:::*',
        'Action': ['s3:GetBucketLocation']},
        {'Effect': 'Allow',
        'Principal': {'AWS': '*'},
        'Resource': 'arn:aws:s3:::*',
        'Action': ['s3:GetBucketLocation']}]}, policy_name='pxua')
        state.list_groups(alias=v1)
        state.list_buckets()
        state.disable_user(alias=v1, user_name=v8)
        state.list_aliases()
        v11 = state.add_user(alias=v1, secret_key='zlgdqjio', user_name='user2')
        state.list_aliases()
        v12 = state.add_user(alias=v1, secret_key='udqitqjg', user_name=v11)
        state.list_groups(alias=v1)
        state.list_buckets()
        state.list_buckets()
        state.disable_user(alias=v1, user_name=v8)
        v13 = state.add_user(alias=v1, secret_key='govvouhh', user_name=v8)
        state.disable_user(alias=v1, user_name=v11)
        state.set_alias(alias='kthw', user_name=v7)
        state.teardown()

if __name__ == '__main__':
    unittest.main()