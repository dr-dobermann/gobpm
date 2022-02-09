# Variables package

Variables package provides functionality of variant Variables and Variable Storages. 
Variables package supports functionality of Process, Message and Task in GoBPM. 

In the same time package Variables could be used as a stand-alone package in case 
there is necessity of use variant variables.

## Variable

Variable is the named data storage. Varable itself is not thread safe. If Variable is used 
in concurrent project with concurrent read and write, it should be protected by 
any lock mechanism. VarStore provides thread safety through embedded Mutex.

### Variable Type

Variable's type is set on the creation and couldn't be changed afterwile. 
Variable type could be checked by Variable's method `Type()` which returns `Type`:

  - Int - keeps values of int64 type

  - Bool - bool type values

  - String - string type values

  - Float - float64 type values

  - Time - time.Time type variables

#### Type conversion

Variable value could present its value into the value of type different from the original one.
For this Variable's methods `Int(), Bool(), StrVal(), Float64(), Time()` could be used.
If something goes wrong while value conversion in these methods the panic will fired.
To check if there is safe conversion or not the Variable's method `CanConvertTo(newType Type)` should
be used.

Conversion table:

| From/To     | int | bool | string | float64 | time.Time |
| ---         |---         |---              |----                          |---                   | --- |
| Int         | ok         | false = 0<br/>true = 1 | if ParseFloat() w/o error ok | math.Round(f) | t.UnixMilli() |
| Bool        | 0 = false<br/>!0 = true | ok | (ToUpper(s) == "TRUE") = true<br/>(ToUpper(s) != "TRUE") = false | 0.0 =  false<br/>!0.0 = true |error |
| StrVal     | ok          | true = "true"<br/>false = "false" | ok | ok | converted to RFC3339 |
| Float64    | ok          | false = 0.0<br/>true  = 1.0 | if ParseFloat() w/o error ok | ok |  float64(UnixMilli(t))
| Time       | UnixMilli() | error  |if time.Parse(RFC3339) w/o error ok | UnixMilli(int64(f)) | ok |

#### Float variable precision

Float variable by default has precision 2. Precision could be updated by `SetPrecision` and check it by `Precision`. 
Float precision is uded on float comparison.

### Variable Value

Variable embeds `Values` structure which is used to keep typed Variable value. The memeber used depends on type of the
Variable. If Variable object is v, then Int stored as v.I, Bool as v.B, String as v.S, Float as v.S and Time as v.T.

Variable has no protection in case of user changed values of Values structure, so please use them only for read.

The user could also get untyped interface{} value of the Variable throug `Value()` method.

### Creating new Variable

Variable could be created by fuction `V(name string, type Type, value interface{})`. This function creates a single variable named
`name` of type `type` and with value `value`. 

`value` could accept any value convertable to type `type`, if it couldn't be converted then default
value of type `type` will be assigned to the Variable. If user want to check if the new Variable has right value without preceded
call of `CanConvertTo` function, she/he could compart typed value of the Variable from `Values` member with the desired one.

New Variable could be created as a copy of existed Variable by Variable's method `Copy`

## VarStore

VarStore is a storage of Variables. It provides name uniqueness between stored Variables. 
To create a new VarStore `NewVarStore` function should be called.

### Managing stored Variables
Adding Variables to the VarStore could be done by New* functions:

  - with untyped value `NewVar`;

  - with typed value: `NewInt`, `NewBool`, `NewString`, `NewFloat` and `NewTime`.
 
Getting variables stored in the VarStore provided by `GetVar` method. If there is no variable with given name, then error returned with nil Variable pointer.

To safe-thread updating stored Variable `Update` method should be called.
**Do not update Variables directly in concurrent environment!**