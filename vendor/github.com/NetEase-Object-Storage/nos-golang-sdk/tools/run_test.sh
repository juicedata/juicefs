#!/bin/bash 

cur=`pwd`
# export GOPATH=$cur/../../../

curPath=`pwd`/..  
for dir in $curPath/*
do
    echo $dir 
    test -d $dir && cd $dir && go test 
done 
