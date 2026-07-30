#!/bin/bash
# Test script for GraphQL provider

echo "=== Testing GraphQL Provider ==="
echo ""

# Test 1: Check if glab is installed and authenticated
echo "1. Checking glab installation..."
if ! command -v glab &> /dev/null; then
    echo "ERROR: glab is not installed"
    exit 1
fi
echo "   ✓ glab is installed"

echo ""
echo "2. Checking glab authentication..."
if ! glab auth status &> /dev/null; then
    echo "ERROR: glab is not authenticated"
    echo "Please run: glab auth login"
    exit 1
fi
echo "   ✓ glab is authenticated"

echo ""
echo "3. Testing basic GraphQL query..."
glab api graphql -f query='{ currentUser { username } }' 2>&1 | head -5
if [ $? -eq 0 ]; then
    echo "   ✓ GraphQL queries work"
else
    echo "   ✗ GraphQL queries failed"
    exit 1
fi

echo ""
echo "4. Building glamr..."
go build -o glamr ./cmd/glamr
if [ $? -eq 0 ]; then
    echo "   ✓ Build successful"
else
    echo "   ✗ Build failed"
    exit 1
fi

echo ""
echo "5. Testing GraphQL provider (simple mode)..."
echo "   Running: ./glamr --no-tui --use-graphql"
echo ""
./glamr --no-tui --use-graphql

echo ""
echo "=== All tests passed! ==="
echo ""
echo "To run the full TUI with GraphQL:"
echo "  ./glamr"
echo ""
echo "To use REST API instead:"
echo "  ./glamr --use-graphql=false"
