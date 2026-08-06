// t44_background: background job and wait
// diagnostics: program prints its result to stdout
go func() {
    fmt.Println("bg")
}()
fmt.Println("main")
