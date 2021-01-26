package com.juicefs.contract;

import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.fs.contract.AbstractContractGetFileStatusTest;
import org.apache.hadoop.fs.contract.AbstractFSContract;
import org.apache.hadoop.fs.Path;


public class TestGetFileStatus extends AbstractContractGetFileStatusTest {
  @Override
  protected AbstractFSContract createContract(Configuration conf) {
      return new JuiceFSContract(conf);
  }

  @Override
  public void setup() throws Exception {
    super.setup();
    getFileSystem().delete(new Path("jfs:///test"));
  }
}
