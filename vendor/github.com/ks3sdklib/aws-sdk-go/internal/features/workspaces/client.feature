# language: en
@workspaces @client
Feature: Amazon WorkSpaces

  I want to use Amazon WorkSpaces

  Scenario: Describing workspaces
    When I call the "DescribeWorkspaces" API
    Then the value at "Workspaces" should be a list

  Scenario: Error handling
    Given I attempt to call the "DescribeWorkspaces" API with:
    | WorkspaceIds | [''] |
    Then I expect the response error code to be "ValidationException"
