# language: en
@emr @client
Feature: Amazon EMR

  Scenario: Making a basic request
    When I call the "DescribeJobFlows" API
    Then the value at "JobFlows" should be a list

  Scenario: Error handling
    When I attempt to call the "DescribeJobFlows" API with:
    | JobFlowIds | ['fake_job_flow'] |
    Then I expect the response error code to be "ValidationException"
    And I expect the response error message to include:
    """
    Specified job flow ID not valid
    """
