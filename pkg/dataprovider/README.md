# Dataprovider package

Package consists of two interfaces for processes' data manipualtion.
Realization of the interfaces `DataItem` and `DataProvider` could encapsulate 
physical data implementation and use whether database, in-memory variables or
any other data source.

## DataItem

DataItem represent single variable or an array of variables of the same type.
DataItem has name, type, and collection(array) attribute.

The DataItem interface is as followed:

    type DataItem interface {
        Name() string
        Type() variables.Type
        IsCollection() bool

        // returns length of the DataItem if it's a collection
        // if DataItem is not a collection, Len returns 1.
        Len() int

        // if DataItem is a collection, then GetOne fires panic
        // use GetSome instead
        GetOne() variables.Variable

        // Retruns a slice of elements if DataItem is a collection.
        // First element has 0 index.
        // If DataItem is not a collection, then panic fired
        GetSome(from, to int) ([]variables.Variable, error)

        // Updates value of the DataItem.
        // if it's a collection, the panic fired.
        UpdateOne(newValue *variables.Variable) error

        // Updates single or range elements of collection.
        // If DataItem is not a collection,
        // the errs.ErrIsNotACollection returned.
        UpdateSome(from, to int, newValues []*variables.Variable) error
    }

## DataProvider

DataProvider represent storage functionality for the set of DataItem. 
It is not allowed to have more that one DataItems with same name inside one DataProvider.
The interface cosists of few methods as followed:

    type DataProvider interface {
        AddDataItem(nv DataItem) error
        GetDataItem(vname string) (DataItem, error)
        DelDataItem(vname string) error
        UpdateDataItem(vn string, newVal DataItem) error
    }