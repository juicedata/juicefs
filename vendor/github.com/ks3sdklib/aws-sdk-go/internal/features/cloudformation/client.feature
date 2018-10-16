# language: en
@cloudformation @client
Feature: AWS CloudFormation

  Scenario: Making a basic request
    When I call the "ListStacks" API
    Then the value at "StackSummaries" should be a list

  Scenario: Error handling
    When I attempt to call the "CreateStack" API with:
    | StackName   | fakestack                       |
    | TemplateURL | http://s3.amazonaws.com/foo/bar |
    Then I expect the response error code to be "ValidationError"
    And I expect the response error message to include:
    """
    TemplateURL must reference a valid S3 object to which you have access.
    """
