# Dataprovider package

Package consists of two interfaces for processes' data manipualtion.
Realization of the interfaces `DataItem` and `DataProvider` could encapsulate 
physical data implementation and use whether database, in-memory variables or
any other data source.

## DataItem

DataItem represents any variable whether single variable, structure, array or map.
DataItem has name. To get underlayed value DataItem provides GetValue method which
returns a json representation of DataItem.

The DataItem interface is as followed:

    type DataItem interface {
        IsCollection() bool

        // returns length of the DataItem if it's a collection
        // if DataItem is not a collection, Len returns 1.
        Len() int

	    // Copy creates and returns an DataItem's copy.
	    Copy() DataItem

        // GetValue returns a map of underlying DataItem data.
        // This method is used on json marshalling of Message content.
        GetValue() map[string]interface{}

        // UpdateValue gets new value nv and trys to update
        // its current state with a nv.
        // It it's imposible error returned.
        // This method is used on json unmarshalling of Message content.
        UpdateValue(nv map[string]interface{}) error
    }

## DataProvider

DataProvider represent storage functionality for the set of DataItem. 
It is not allowed to have more that one DataItem with the same name inside one DataProvider.
The interface cosists of few methods as followed:

    type DataProvider interface {
        AddDataItem(item DataItem) error
        GetDataItem(name string) (DataItem, error)
        DelDataItem(name string) error
    }