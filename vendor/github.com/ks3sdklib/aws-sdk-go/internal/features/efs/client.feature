# language: en
@efs @client
Feature: Amazon Elastic File System

  I want to use Amazon Elastic File System

  Scenario: Listing file systems
    When I call the "DescribeFileSystems" API
    Then the value at "FileSystems" should be a list

  Scenario: Error handling
    Given I attempt to call the "DeleteFileSystem" API with:
    | FileSystemId | fake-id |
    Then I expect the response error code to be "ValidationException"
