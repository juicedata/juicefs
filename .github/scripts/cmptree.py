#!/usr/bin/env python

# Copyright (c) 2015, Bill Zissimopoulos. All rights reserved.
#
# Redistribution  and use  in source  and  binary forms,  with or  without
# modification, are  permitted provided that the  following conditions are
# met:
#
# 1.  Redistributions  of source  code  must  retain the  above  copyright
# notice, this list of conditions and the following disclaimer.
#
# 2. Redistributions  in binary  form must  reproduce the  above copyright
# notice,  this list  of conditions  and the  following disclaimer  in the
# documentation and/or other materials provided with the distribution.
#
# 3.  Neither the  name  of the  copyright  holder nor  the  names of  its
# contributors may  be used  to endorse or  promote products  derived from
# this software without specific prior written permission.
#
# THIS SOFTWARE IS PROVIDED BY  THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS
# IS" AND  ANY EXPRESS OR  IMPLIED WARRANTIES, INCLUDING, BUT  NOT LIMITED
# TO,  THE  IMPLIED  WARRANTIES  OF  MERCHANTABILITY  AND  FITNESS  FOR  A
# PARTICULAR  PURPOSE ARE  DISCLAIMED.  IN NO  EVENT  SHALL THE  COPYRIGHT
# HOLDER OR CONTRIBUTORS  BE LIABLE FOR ANY  DIRECT, INDIRECT, INCIDENTAL,
# SPECIAL,  EXEMPLARY,  OR  CONSEQUENTIAL   DAMAGES  (INCLUDING,  BUT  NOT
# LIMITED TO,  PROCUREMENT OF SUBSTITUTE  GOODS OR SERVICES; LOSS  OF USE,
# DATA, OR  PROFITS; OR BUSINESS  INTERRUPTION) HOWEVER CAUSED AND  ON ANY
# THEORY  OF LIABILITY,  WHETHER IN  CONTRACT, STRICT  LIABILITY, OR  TORT
# (INCLUDING NEGLIGENCE  OR OTHERWISE) ARISING IN  ANY WAY OUT OF  THE USE
# OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

import filecmp, os

class TreeComparator(object):
    def __init__(self, dir1, dir2):
        self.dir1 = dir1
        self.dir2 = dir2
        self.left_only = []
        self.right_only = []
        self.common_funny = []
        self.funny_files = []
        self.diff_files = []
    def compare(self, p=""):
        d1 = os.path.join(self.dir1, p)
        d2 = os.path.join(self.dir2, p)
        dcmp = filecmp.dircmp(d1, d2, ignore=[])
        self.left_only.extend(os.path.join(p, n) for n in dcmp.left_only)
        self.right_only.extend(os.path.join(p, n) for n in dcmp.right_only)
        self.common_funny.extend(os.path.join(p, n) for n in dcmp.common_funny)
        self.funny_files.extend(os.path.join(p, n) for n in dcmp.funny_files)
        (match, mismatch, errors) = filecmp.cmpfiles(d1, d2, dcmp.common_files, shallow=False)
        self.diff_files.extend(os.path.join(p, n) for n in mismatch)
        self.funny_files.extend(os.path.join(p, n) for n in errors)
        for d in dcmp.common_dirs:
            self.compare(os.path.join(p, d))

if "__main__" == __name__:
    import argparse, sys
    def info(s):
        print ("%s: %s" % (os.path.basename(sys.argv[0]), s))
    def warn(s):
        print ("%s: %s" % (os.path.basename(sys.argv[0]), s))
    def fail(s, exitcode = 1):
        warn(s)
        sys.exit(exitcode)
    def main():
        p = argparse.ArgumentParser()
        p.add_argument("-q", "--quiet", action="store_true")
        p.add_argument("dir1")
        p.add_argument("dir2")
        args = p.parse_args(sys.argv[1:])
        tcmp = TreeComparator(args.dir1, args.dir2)
        tcmp.compare()
        res = len(tcmp.left_only) + len(tcmp.right_only) + \
            len(tcmp.common_funny) + len(tcmp.funny_files) + len(tcmp.diff_files)
        if not args.quiet:
            if tcmp.left_only:
                print ("Left only:")
                for n in tcmp.left_only:
                    print( "    %s" % n)
            if tcmp.right_only:
                print ("Right only:")
                for n in tcmp.right_only:
                    print( "    %s" % n)
            if tcmp.funny_files:
                print ("Funny files:")
                for n in tcmp.funny_files:
                    print( "    %s" % n)
            if tcmp.common_funny:
                print ("Differing stats:")
                for n in tcmp.common_funny:
                    print ("    %s" % n)
            if tcmp.diff_files:
                print ("Differing files:")
                for n in tcmp.diff_files:
                    print ("    %s" % n)
        sys.exit(int(0 < res))
    def __entry():
        try:
            main()
        except EnvironmentError as ex:
            fail(ex)
        except KeyboardInterrupt:
            fail("interrupted", 130)
    __entry()