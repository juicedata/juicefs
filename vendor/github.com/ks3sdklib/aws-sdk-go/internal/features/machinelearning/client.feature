# language: en
@machinelearning @client
Feature: Amazon Machine Learning

  I want to use Amazon Machine Learning

  Scenario: Describing MLModels
    When I call the "DescribeMLModels" API
    Then the value at "Results" should be a list

  Scenario: Error handling
    When I attempt to call the "GetBatchPrediction" API with:
    | BatchPredictionId | non-exist |
    Then I expect the response error code to be "ResourceNotFoundException"
