# VarDataProvider

`vardataprovider` package implements `DataItem` and `DataProvider` interfaces from 
`dataprovider` package based on `Variable` and `VarStore` types from `variables`
package.

# DataItem for Variable

DataItem implementation for Variable presented by `variableDataItem` type. Since
variableDataItem isn't exported type, the only way to create an item of the type
is `NewDI` function call.

    di := vardataprovider.NewDI(v)

where v is the `variables.Variable` source variable.

`variableDataItem` holds single varable. The name of DataItem name is the same as the 
source variable and its length is 1. It's retrun false on IsCollection request and returns 
`errs.ErrIsNotACollection` error on array-related functions `GetSome` and `UpdateSome`.

# DataProvider for VarStore

`DataProvider` interface implemented for `varStoreDataProvider` type.
To create an object of the varStoreDataProvider the `vardataprovider.New()` function
should be called.

    vdp := vardataprovider.New()

