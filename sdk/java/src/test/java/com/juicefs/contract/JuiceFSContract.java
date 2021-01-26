package com.juicefs.contract;

import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.fs.contract.AbstractBondedFSContract;

public class JuiceFSContract extends AbstractBondedFSContract {

  public JuiceFSContract(Configuration conf) {
    super(conf);
    addConfResource("contract/juicefs.xml");
  }

  @Override
  public String getScheme() {
    return "jfs";
  }

}
