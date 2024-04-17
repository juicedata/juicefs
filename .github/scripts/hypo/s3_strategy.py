from hypothesis import strategies as st
from string import ascii_lowercase

MAX_OBJECT_SIZE=10*1024*1024
# https://min.io/docs/minio/linux/administration/identity-access-management/policy-based-access-control.html#minio-policy-actions
S3_ACTION_LIST = ["s3:*", "s3:DeleteObject", "s3:GetObject","s3:ListBucket","s3:PutObject", "s3:PutObjectTagging", "s3:GetObjectTagging", "s3:DeleteObjectTagging"]
st_user_name = st.sampled_from(['user1', 'user2', 'user3'])
st_group_name = st.sampled_from(['group1', 'group2', 'group3'])
st_group_members = st.lists(st_user_name, max_size=3, unique=True)
st_secret_key = st.text(alphabet=ascii_lowercase, min_size=8, max_size=8)
st_alias_name = st.text(alphabet=ascii_lowercase, min_size=4, max_size=4)
st_bucket_name = st.text(alphabet=ascii_lowercase, min_size=4, max_size=4)
st_object_name = st.text(alphabet=ascii_lowercase, min_size=4, max_size=4)
st_object_prefix = st.text(alphabet=ascii_lowercase, min_size=1, max_size=1)
st_content = st.binary(min_size=0, max_size=MAX_OBJECT_SIZE)
st_part_size = st.sampled_from([5*1024*1024, 8*1024*1024])
st_offset = st.integers(min_value=0, max_value=MAX_OBJECT_SIZE)
st_length = st.integers(min_value=0, max_value=MAX_OBJECT_SIZE)
st_policy_name = st.text(alphabet=ascii_lowercase, min_size=4, max_size=4)
st_policy = st.fixed_dictionaries({
    "Version": st.just("2012-10-17"),
    "Statement": st.lists(
        st.fixed_dictionaries({
            "Effect": st.sampled_from(["Allow", "Deny"]),
            "Principal": st.fixed_dictionaries({"AWS": st.just("*")}),
            "Action": st.lists(
                st.sampled_from(S3_ACTION_LIST),
                min_size=1, max_size=3,
                unique=True
            ),
            "Resource": st.just("arn:aws:s3:::*"),
        }),
        min_size=1, max_size=3
    )
})
