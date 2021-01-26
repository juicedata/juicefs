package com.juicefs.contract;

import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.fs.contract.AbstractContractCreateTest;
import org.apache.hadoop.fs.contract.AbstractFSContract;

public class TestCreate extends AbstractContractCreateTest {
  @Override
  protected AbstractFSContract createContract(Configuration conf) {
      return new JuiceFSContract(conf);
  }
}
