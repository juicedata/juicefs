import unittest
from s3 import S3Machine

class TestS3(unittest.TestCase):
    def test_s3(self):
        state = S3Machine()
        state.create_bucket('bucket1')
        state.create_bucket('bucket2')
        state.put_object('bucket1', 'object1')
        state.put_object('bucket1', 'object2')
        state.remove_object('bucket1:object1')
        state.remove_object('bucket1:object2')
        state.teardown()

if __name__ == '__main__':
    unittest.main()