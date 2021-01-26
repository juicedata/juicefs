package com.juicefs;

import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.fs.DelegateToFileSystem;
import org.apache.hadoop.fs.FileSystem;

import java.io.IOException;
import java.net.URI;
import java.net.URISyntaxException;

public class JuiceFS extends DelegateToFileSystem {
    JuiceFS(final URI uri, final Configuration conf) throws IOException, URISyntaxException {
        super(uri, FileSystem.get(uri, conf), conf, uri.getScheme(), false);
    }

    @Override
    public int getUriDefaultPort() {
        return -1;
    }
}
