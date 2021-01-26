package com.juicefs.contract;

import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.fs.contract.AbstractContractMkdirTest;
import org.apache.hadoop.fs.contract.AbstractFSContract;

public class TestMkdir extends AbstractContractMkdirTest {
  @Override
  protected AbstractFSContract createContract(Configuration conf) {
      return new JuiceFSContract(conf);
  }
}
