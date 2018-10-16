# language: en
@cognitosync @client
Feature: Amazon Cognito Sync

  Scenario: Making a basic request
    When I call the "ListIdentityPoolUsage" API
    Then the value at "IdentityPoolUsages" should be a list

  Scenario: Error handling
    When I attempt to call the "DescribeIdentityPoolUsage" API with:
    | IdentityPoolId | us-east-1:aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee |
    Then I expect the response error code to be "ResourceNotFoundException"
    And I expect the response error message to include:
    """
    IdentityPool 'us-east-1:aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee' not found
    """
