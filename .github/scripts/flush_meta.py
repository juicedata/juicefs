import argparse
import os
from posixpath import expanduser
from utils import *

if __name__ == "__main__":
    p = argparse.ArgumentParser()
    p.add_argument("meta_url")
    args = p.parse_args(sys.argv[1:])
    flush_meta(args.meta_url)