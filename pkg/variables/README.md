# Variables package

Variables package provides functionality of variant Variables and Variable Storages. 
Variables package supports functionality of Process, Message and Task in GoBPM. 

In the same time package Variables could be used as a stand-alone package in case 
there is necessity of use variant variables.

## Variable

Variable is named data storage. Varable is not thread safe. If Variable is used 
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

---
| From/To     | int        | bool            | string                       | float64              | time.Time |
---
| Int         | ok         | ok              | if ParseFloat() w/o error ok | ok                   | ok |
--
| Bool        | 0 = false<br/>!0 = true | ok | (ToUpper(s) == "TRUE") = true<br/>(ToUpper(s) != "TRUE") = false | 0.0 =  false<br/>!0.0 = true |error |
---                                              
| StrVal     | ok          | true = "true"<br/>false = "false" | ok | ok | converted to RFC3339 |
---
| Float64    | ok          | false = 0.0<br/>true  = 1.0 | if ParseFloat() w/o error ok | ok |  float64(UnixMilli())
---
| Time       | UnixMilli() | error  |if time.Parse(RFC3339) w/o error ok | UnixMilli(int64()) | ok |
---

Variable could be created by fuction `V(name string, type Type, value interface{})`. This function creates a single variable named
`name` of type `type` and with value `value`. 

`value` could accept any value convertable to type `type`, if not the default
value of type `type` will be assigned to the Variable.


