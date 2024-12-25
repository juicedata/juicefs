from setuptools import setup, find_packages

# The following line will be replaced by the actual version number during the Make process
VERSION = "1.3.0"
BUILD_INFO = "BUILDDATE+COMMIT HASH"


setup(
    name='juicefs',
    version=VERSION,
    description=BUILD_INFO,
    package_data={'juicefs': ['*.so']},
    packages=find_packages(where="."),
    include_package_data=True,
    install_requires=['six'],
    entry_points={
        'fsspec.specs': [
            'jfs = juicefs.JuiceFS',
        ],
    },
)
