# GEP is GoBPM Expression Processor

GEP is an internal, extensible, API-aimed expression processor. GEP implements the 
Expression interface. GEP expression is conveyer of operations. Every operation 
could use results of previous operation or take its-own parameters.

The result of GEP is a single Variable. GEP doesn't provide access to 
intermediate operations results, but it could be easily implemented in case 
of necessity.

## Usage

To create the GEP instance `New` package function should be called. It returns
new GEP object in `Created` state. Then one or more operation could be added
to conveyer.

After all the necessary operations were added, `Evaluate` GEP's method should be called.
If it returns no error, `GetResult` could be called to get the GEP's results.

If there is necessity to make multiple sequential calls of Evaluate, then before
every next call, except first one, `UpdateState` of the GEP's object should be 
called with `expression.Parametrized` state.

### GEP Operations

GEP's operation defined as an `OpFunc` and `ParameterLoader`. OpFunc is the main 
workhorse of GEP. Any function which comply OpFunc signature 
`func(x *variables.Variable) (*variables.Variabale, error)` could be used as an operation.

OpFunc returns a calculated result as a *variables.Variable. If there is any error, nil 
pointer to variable.Variables returned.

The result of successfully called OpFunc saves as GEP result.

If ParameterLoader of Operation is nil, then the current GEP result is used as 
an OpFunc parameter. 

## x = x op y function framework

GEP provides framwork for adding extension operation as x = x op y, where op is an any
binary function.

To use the framework the `FunctionDefinition` object should be created. It takes one 
`OpFuncGenerator` which generates a bunch of OpFunc for every supported x type. 

If `EmptyParamAllowed` flag of `FunctionDefinition` is set to true, then y parameter could
be ommitted.

To check eligibility of y parameter one or more checking function could be added to 
`Function Definition` in `Checkers` field. If the Checker list is empty, then no test
applyed on y.

After the proper filling of `FunctionDefinition`, it should be registered by 
`AddOpFuncDefinition` function call. If it's ok, then concrete function could be 
returned by `GetOpFunc`.

### Available functions

Based on this framework followed operation were created:

  - algebraic base operations: substraction(Sub), sum(Sum), multiplication(Mul) and
  Dividing(Div)

  - comparation operations: Equal, NotEqual, Less, Greater, LessOrEqual, GreaterOrEqual

## Extendability

GEP provides the tools for easily adding new functions. These techniques were
demonstrated in `examples/gep` direcrory. 

In the example two functions were added:

  - MathFuncCaller -- the shell for any function with signature `func(float64 x) float64`, including most
  of math-package function. Example shows usage of math.Sqrt function.

  - CheckIfExists -- function which finds index of element, equal to x parameter, in a list of variables.

