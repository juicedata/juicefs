package com.juicefs.contract;

import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.fs.contract.AbstractContractSeekTest;
import org.apache.hadoop.fs.contract.AbstractFSContract;
import org.apache.hadoop.fs.Path;

public class TestSeek extends AbstractContractSeekTest {
  @Override
  protected AbstractFSContract createContract(Configuration conf) {
      return new JuiceFSContract(conf);
  }

  @Override
  public void teardown() throws Exception {
    getFileSystem().delete(path("bigseekfile.txt"));
    super.teardown();
  }
}
