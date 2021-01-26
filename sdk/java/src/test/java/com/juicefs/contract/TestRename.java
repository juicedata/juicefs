package com.juicefs.contract;

import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.fs.contract.AbstractContractRenameTest;
import org.apache.hadoop.fs.contract.AbstractFSContract;
import org.apache.hadoop.fs.Path;


public class TestRename extends AbstractContractRenameTest {
  @Override
  protected AbstractFSContract createContract(Configuration conf) {
      return new JuiceFSContract(conf);
  }
}
