import unittest
from s3 import S3Machine

class TestS3(unittest.TestCase):
    def test_s3(self):
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

    def test_s3_policy(self):
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


    def test_s3_user(self):
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
        
    def test_s3_group(self):
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

if __name__ == '__main__':
    unittest.main()