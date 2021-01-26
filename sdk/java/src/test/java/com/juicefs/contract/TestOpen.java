package com.juicefs.contract;

import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.fs.contract.AbstractContractOpenTest;
import org.apache.hadoop.fs.contract.AbstractFSContract;

public class TestOpen extends AbstractContractOpenTest {
  @Override
  protected AbstractFSContract createContract(Configuration conf) {
      return new JuiceFSContract(conf);
  }
}
