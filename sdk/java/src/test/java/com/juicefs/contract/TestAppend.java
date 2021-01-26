package com.juicefs.contract;

import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.fs.contract.AbstractContractAppendTest;
import org.apache.hadoop.fs.contract.AbstractFSContract;
import org.apache.hadoop.fs.Path;
import org.apache.hadoop.fs.FileSystem;

import org.junit.Test;
import org.apache.hadoop.fs.FSDataOutputStream;
import org.apache.hadoop.fs.contract.ContractTestUtils;

import static org.apache.hadoop.fs.contract.ContractTestUtils.cleanup;
import static org.apache.hadoop.fs.contract.ContractTestUtils.createFile;
import static org.apache.hadoop.fs.contract.ContractTestUtils.dataset;
import static org.apache.hadoop.fs.contract.ContractTestUtils.touch;


public class TestAppend extends AbstractContractAppendTest {
  @Override
  protected AbstractFSContract createContract(Configuration conf) {
      return new JuiceFSContract(conf);
  }

  @Override
  public void teardown() throws Exception {
    getFileSystem().delete(new Path(path("test"), "target"));
    super.teardown();
  }
}
