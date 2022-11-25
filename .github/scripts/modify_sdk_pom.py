#!/usr/bin/env python
# -*- encoding: utf-8 -*-
import os
import re


def get_plugin_str(taget_tests, taget_classes, time_constant):
    s = """ <plugin>
				<groupId>org.pitest</groupId>
				<artifactId>pitest-maven</artifactId>
				<version>1.9.11</version>
				<configuration>
					<targetClasses>
						<param>{taget_classes}</param>
					</targetClasses>
					<targetTests>
						<param>{taget_tests}</param>
					</targetTests>
					<timeoutConstant>{time_constant}</timeoutConstant>
				</configuration>
			</plugin> """
    s = s.replace('{taget_classes}', taget_classes)
    s = s.replace('{taget_tests}', taget_tests)
    s = s.replace('{time_constant}', time_constant)
    return s

def modify_pom(pom_path, taget_tests, taget_classes, time_constant):
    new_lines = []
    with open(pom_path, 'r') as f:
        for line in f.readlines():
            if line.strip() == '</plugins>':
                new_lines.append(get_plugin_str(taget_tests, taget_classes, time_constant)+'\n')
            new_lines.append(line)
    with open(pom_path, 'w') as f:
        f.writelines(new_lines)

if __name__ == '__main__':
    pom_path = os.environ['POM_XML_PATH']
    taget_tests = os.environ['TARGET_TESTS']
    taget_classes = os.environ['TARGET_CLASSES']
    time_constant = os.environ['TIME_CONSTANT']
    modify_pom(pom_path, taget_tests, taget_classes, time_constant)
    