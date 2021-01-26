package com.juicefs.contract;

import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.fs.contract.AbstractContractSetTimesTest;
import org.apache.hadoop.fs.contract.AbstractFSContract;

public class TestSetTimes extends AbstractContractSetTimesTest {
  @Override
  protected AbstractFSContract createContract(Configuration conf) {
      return new JuiceFSContract(conf);
  }
}
