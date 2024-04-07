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

    def test_policy_remove(self):
        state = S3Machine()
        v1 = state.init_aliases()
        v2, v3, v4, v5 = state.init_policies()
        state.remove_policy(alias=v1, policy_name=v3)
        state.list_groups(alias=v1)
        state.remove_policy(alias=v1, policy_name=v2)
        state.policy_info(alias=v1, policy_name=v5)
        state.teardown()

if __name__ == '__main__':
    unittest.main()