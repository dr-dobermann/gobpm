# model package

This package consists of definitions of the BPMN elements.

### Data manipulation in goBPM

BPMN v2 standard recommends using pair `ItemAvareElement -> DataDefinition` to 
define single or multiple (isCollection == true) process/task variable or property.

goBPM takes similar approach:
  1. `ItemDefinition` - defines type, collection or single value and data manipulation
  interface DataAccessor, which incapsulated direct data manipulation.

  2. `ItemAwareElement` - named data element used in the BPM process.

  3. `DataSet` - class covered pairs InputSet/DataInput and OutputSet/DataOutput.

`InputOutputSpecification` - covers input and ouput specification for tasks and processes.
