# language: en
@cloudwatch @client
Feature: Amazon CloudWatch

  Scenario: Making a basic request
    When I call the "ListMetrics" API with:
    | Namespace | AWS/EC2 |
    Then the value at "Metrics" should be a list

  Scenario: Error handling
    When I attempt to call the "SetAlarmState" API with:
    | AlarmName   | abc |
    | StateValue  | mno |
    | StateReason | xyz |
    Then I expect the response error code to be "ValidationError"
    And I expect the response error message to include:
    """
    failed to satisfy constraint
    """
