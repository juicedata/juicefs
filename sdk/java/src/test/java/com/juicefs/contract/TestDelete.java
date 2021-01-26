package com.juicefs.contract;

import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.fs.contract.AbstractContractDeleteTest;
import org.apache.hadoop.fs.contract.AbstractFSContract;

public class TestDelete extends AbstractContractDeleteTest {
  @Override
  protected AbstractFSContract createContract(Configuration conf) {
      return new JuiceFSContract(conf);
  }
}
