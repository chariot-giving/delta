package maintenance

// NamespaceMaintainer runs regular maintenance operations against resource namespaces,
// like cleaning up old resources or expiring resources. It runs only on the client which
// has been elected leader at any given time.
//
// Its methods are not safe for concurrent use.
type NamespaceMaintainer interface {
}
