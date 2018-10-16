# language: en
@directoryservice @client
Feature: AWS Directory Service

  I want to use AWS Directory Service

  Scenario: Listing directories
    When I call the "DescribeDirectories" API
    Then the value at "DirectoryDescriptions" should be a list

  Scenario: Error handling
    When I attempt to call the "CreateDirectory" API with:
    | Name     | |
    | Password | |
    | Size     | |
    Then I expect the response error code to be "ValidationException"
