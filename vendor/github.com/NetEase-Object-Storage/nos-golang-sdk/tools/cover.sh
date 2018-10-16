#!/bin/bash 
# used in converage html generation for golang project
# put this shell in project's dir 
curPath=`pwd`
# export GOPATH=$curPath/../../..
ROOT_DIR=`pwd`/.. 
COVERAGE_FILE=`pwd`/coverage.html # filepath to put converage 

##package need to ignore 
declare -a ignorePackage=("github.com" "gopkg.in" "logger" "tools" ,"test") 

subdirs=`ls $ROOT_DIR`
prefix='{"Packages":['
suffix=']}'
empty_result='{"Packages":null}'
left=${#prefix} ##left postion of json result for a package 

#
# check and cd package if package legall 
# 
check_and_cd_package()
{
  pkg=$1
  curDir=$ROOT_DIR/$pkg 
  #echo $pkg
  #echo $ROOT_DIR
  echo "curDir:"$curDir
  for ipkg in ${ignorePackage[@]};
  do 
#      echo "echo $pkg | grep $ipkg"
      info=`echo $pkg| grep $ipkg`
      if [ "$info" != "" ]; then 
#         echo "ignorePackage $pkg"
          return 1
      fi 
  done 
  isDir=`test -d $curDir`
  if [ "$?" != "0" ];then 
        return 1
  fi 
  cd $curDir
  return $?
}

#
# get current package's converge 
#
get_package_coverage(){

    result=`gocov test`
    
    if [ "$?" != "0" ] || [ "$result" == "" ] ; then 
         return 1
    fi 
    if [ "$result" == "$empty_result" ]; then
         return 1
    fi 
 
    right=$((${#result}-${#suffix}-$left))     
    cur_result=${result:$left:$right}
    echo $cur_result
    return 0
}


json_result=""
for pkg in $subdirs;
do
    check_and_cd_package $pkg

    if [ "$?" != "0" ] ; then
        continue
    fi 
    #cd $curDir
    cur_result=$(get_package_coverage)
    if [ "$?" != "0" ] ; then
        continue
    fi 
 
    echo "Get package $pkg's coverage success"

    if [ "$json_result" != "" ]; then 
         json_result="$json_result,"
    fi
 
    json_result=$json_result$cur_result  
    #echo "*************current result***************"
    #echo $cur_result 
    #echo "***************current result end*********"
    #echo "PSW:"$ROOT_DIR
done 

#echo "-----------------------"

total_result=$prefix$json_result$suffix
echo $total_result | gocov-html > $COVERAGE_FILE
#echo $total_result
